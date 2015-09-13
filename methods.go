package appkit

import (
	"fmt"
	"encoding/json"
	"time"
	"sync"

	db "github.com/theduke/go-dukedb"
)

type Method struct {
	Name         string
	RequiresUser bool
	Run func(a *App, r ApiRequest, unblock func()) ApiResponse
}

type methodInstance struct {
	method *Method

	request ApiRequest

	responder func(ApiResponse)
	
	createdAt time.Time	
	startedAt time.Time
	finishedAt time.Time

	blocked bool
	stale bool
}

func (m methodInstance) IsRunning() bool {
	return !m.startedAt.IsZero()
}

type methodQueue struct {
	app *App

	sync.Mutex

	queue map[*methodInstance]bool

	maxQueued int
	maxRunning int
	maxPerMinute int
	timeout int

	lastAction time.Time
}

func newMethodQueue(m *SessionManager) *methodQueue {
	return &methodQueue{
		app: m.app,
		queue: make(map[*methodInstance]bool),
		maxQueued: m.maxQueued,
		maxRunning: m.maxRunning,
		maxPerMinute: m.maxPerMinute,
		timeout: m.timeout,
		lastAction: time.Now(),
	}
}

func (m *methodQueue) TimeSinceActive() int {
	secs := time.Now().Sub(m.lastAction).Seconds()
	return int(secs)
}

func (m *methodQueue) Count() int {
	return len(m.queue)
}

func (m *methodQueue) CountActive() int {
	count := 0
	for method := range m.queue {
		if method.IsRunning() {
			count++
		} else {
			break
		}
	}

	return count
}

func (m *methodQueue) CountAddedSince(seconds int) int {
	now := time.Now()
	count := 0
	for method := range m.queue {
		if now.Sub(method.createdAt).Seconds() <= float64(seconds) {
			count++
		}
	}

	return count
}

func (m *methodQueue) Add(method *methodInstance) ApiError {
	m.lastAction = time.Now()

	if len(m.queue) >= m.maxQueued {
		return Error{
			Code: "max_methods_queued",
			Message: "The maximum amount of methods is already running",
		}
	}

	if m.CountAddedSince(60) >= m.maxPerMinute {
		return Error{
			Code: "max_methods_per_minute",
			Message: "You have reached the maximum methods/minute limit.",
		}
	}

	m.Lock()
	m.queue[method] = true
	m.Unlock()

	return nil
}
 
// Remove methods that have exceeded the timeout.
func (m *methodQueue) PruneStaleMethods() {
	now := time.Now()

	for method := range m.queue {
		if !method.stale && method.IsRunning() && now.Sub(method.startedAt).Seconds() > float64(m.timeout) {
			m.Lock()
			method.stale = true
			m.Unlock()
		}
	}
}

func (m *methodQueue) CanProcess() bool {
	m.PruneStaleMethods()

	running := 0
	for method := range m.queue {
		if method.IsRunning() && !method.stale {
			if method.blocked {
				return false
			}
			running++	
		}
	}

	if running >= m.maxRunning {
		return false
	}

	return true
}


func (m *methodQueue) Next() *methodInstance {
	for method := range m.queue {
		if method.IsRunning() || method.stale {
			continue
		}
		return method
	}

	return nil
}

func (m *methodQueue) Process() {
	if !m.CanProcess() {
		return
	}

	m.Lock()
	next := m.Next()

	if next == nil {
		m.Unlock()
		return
	}

	next.startedAt = time.Now()
	m.Unlock()

	go func(method *methodInstance) {

		// Recover from panic.
		defer func() {
			rawErr := recover()
			if rawErr != nil {
				// Panic occurred, finish with error response.
				resp := &Response{
					Error: Error{
						Code: "method_panic",
					},
				}
				if err, ok := rawErr.(error); ok {
					resp.Error.AddError(err)
				}

				m.Finish(method, resp)
			}
		}()

		// Run method.
		resp := method.method.Run(m.app, method.request, func() {
			method.blocked = false
		})

		m.Finish(method, resp)
	}(next)
}

func (m *methodQueue) Finish(method *methodInstance, response ApiResponse) {
	// Send the response.
	
	// Recover a panic in the responder.	
	defer func() {
		err := recover()
		if err != nil {
			// Responder paniced!

			// Remove method from queue.
			m.Lock()
			delete(m.queue, method)
			m.Unlock()
			
			method.finishedAt = time.Now()
		}
	}()

	// Send the response.
	method.responder(response)

	// Remove method from queue.
	m.Lock()
	delete(m.queue, method)
	m.Unlock()
	
	method.finishedAt = time.Now()
}

type SessionManager struct {
	app *App

	sync.Mutex

	queues map[ApiSession]*methodQueue

	maxQueued int
	maxRunning int
	maxPerMinute int
	timeout int

	sessionTimeout int
	pruneInterval int
}

func NewSessionManager(app *App) *SessionManager {
	return &SessionManager{
		app: app,
		maxQueued: app.Config.UInt("methods.maxQueued", 30),
		maxRunning: app.Config.UInt("methods.maxRunning", 5),
		maxPerMinute: app.Config.UInt("methods.maxPerMinute", 100),
		timeout: app.Config.UInt("methods.timeout", 30),

		sessionTimeout: app.Config.UInt("sessions.sessionTimeout", 60 * 4),
		pruneInterval: app.Config.UInt("sessions.pruneInterval", 60 * 5),
	}
}

func (m *SessionManager) AddMethod(session ApiSession, method *methodInstance) ApiError {
	queue := m.queues[session]
	if queue == nil {
		m.Lock()
		m.queues[session] = newMethodQueue(m)
		m.Unlock()
		queue = m.queues[session]
	}

	return queue.Add(method)
}

func (m *SessionManager) Prune() {
	m.Lock()
	for session, queue := range m.queues {
		if queue.Count() == 0 && queue.TimeSinceActive() >= m.sessionTimeout {
			delete(m.queues, session)
		}
	}
	m.Unlock()
}

func (m *SessionManager) Run() {
	go func() {
		m.Prune()
		time.Sleep(time.Duration(m.pruneInterval) * time.Second)
	}()
}

type ResourceMethodData struct {
	Resource ApiResource
	Objects []db.Model
	IDs []string
	Query *db.Query
}

func buildResourceMethodData(app *App, rawData interface{}) (*ResourceMethodData, ApiError) {
	if data, ok := rawData.(ResourceMethodData); ok {
		return &data, nil
	}
	methodData :=  &ResourceMethodData{}

	data, ok := rawData.(map[string]interface{})
	if !ok {
		return nil, Error{
			Code: "invalid_data_map_expected",
			Message: "Data must contain a map/dict",
		}
	}

	// Try to build model objects.
	resourceName, _ := data["resource"].(string)
	if resourceName == "" {
		return nil, Error{
			Code: "resource_missing",
			Message: "Data must contain a 'resource' key",
		}
	}

	resource := app.GetResource(resourceName)
	if resource == nil {
		return nil, Error{
			Code: "unknown_resource",
			Message: fmt.Sprintf("The resource %v is not registered", resourceName),
		}
	}
	methodData.Resource = resource

	if rawIds, ok := data["ids"].([]interface{}); ok {
		ids := make([]string, 0)
		for index, rawId := range rawIds {
			id, ok := rawId.(string)
			if !ok {
				return nil, Error{
					Code: "invalid_id",
					Message: fmt.Sprintf("The %vth id '%v' must be a string", index, rawId),
				}
			}

			ids = append(ids, id)
		}

		methodData.IDs = ids
	}

	if objectData, ok := data["objects"]; ok {
		// Objects key exists, try to parse it.
		
		if objects, ok := objectData.([]db.Model); ok {
			// Objects are already a model slice.
			methodData.Objects = objects
		} else {
			// Try to unmarshal the data.
			rawObjects, ok := data["objects"].([]interface{})
			if !ok {
				return nil, Error{
					Code: "invalid_object_data",
					Message: "Expected array in key 'objects'",
				}
			}

			for index, rawObj := range rawObjects {
				js, err := json.Marshal(rawObj)
				if err != nil {
					return nil, Error{
						Code: "json_error",
						Message: err.Error(),
						Errors: []error{err},
					}
				}

				model := resource.NewModel()
				if err := json.Unmarshal(js, model); err != nil {
					return nil, Error{
						Code: "json_unmarshal_error",
						Message: fmt.Sprintf("Could not unmarshal model %v: %v", index, err),
						Errors: []error{err},
					}
				}

				methodData.Objects = append(methodData.Objects, model)
			}
		}
	}

	// Build query.
	if rawQuery, ok := data["query"].(map[string]interface{}); ok {
		query, err := db.ParseQuery(resourceName, rawQuery)
		if err != nil {
			return nil, Error{
				Code: "invalid_query",
				Message: fmt.Sprintf("Error while parsing query: %v", err),
				Errors: []error{err},
			}
		}
		methodData.Query = query
	}

	return methodData, nil
}

func createMethod() *Method {
	return &Method{
		Name: "create",
		Run: func(a *App, r ApiRequest, unblock func()) ApiResponse {
			methodData, err := buildResourceMethodData(a, r.GetData())
			if err != nil {
				return &Response{
					Error: err,
				}
			}

			if methodData.Objects == nil || len(methodData.Objects) < 1 {
				return NewErrorResponse("no_objects", "No objects sent in data.objects")
			}
			if len(methodData.Objects) != 1 {
				return NewErrorResponse("only_one_object_allowed", "")
			}

			return methodData.Resource.ApiCreate(methodData.Objects[0], r)
		},
	}
}

func updateMethod() *Method {
	return &Method{
		Name: "update",
		Run: func(a *App, r ApiRequest, unblock func()) ApiResponse {
			methodData, err := buildResourceMethodData(a, r.GetData())
			if err != nil {
				return &Response{
					Error: err,
				}
			}

			if methodData.Objects == nil || len(methodData.Objects) < 1 {
				return NewErrorResponse("no_objects", "No objects sent in data.objects")
			}
			if len(methodData.Objects) != 1 {
				return NewErrorResponse("only_one_object_allowed", "")
			}

			return methodData.Resource.ApiUpdate(methodData.Objects[0], r)
		},
	}
}

func deleteMethod() *Method {
	return &Method{
		Name: "delete",
		Run: func(a *App, r ApiRequest, unblock func()) ApiResponse {
			methodData, err := buildResourceMethodData(a, r.GetData())
			if err != nil {
				return &Response{
					Error: err,
				}
			}

			if methodData.IDs == nil || len(methodData.IDs) < 1 {
				return NewErrorResponse("no_ids", "No ids sent in data.ids")
			}
			if len(methodData.IDs) != 1 {
				return NewErrorResponse("only_one_object_allowed", "")
			}

			return methodData.Resource.ApiDelete(methodData.IDs[0], r)
		},
	}
}

func queryMethod() *Method {
	return &Method{
		Name: "query",
		Run: func(a *App, r ApiRequest, unblock func()) ApiResponse {
			methodData, err := buildResourceMethodData(a, r.GetData())
			if err != nil {
				return &Response{
					Error: err,
				}
			}

			if methodData.Query == nil {
				return NewErrorResponse("no_query", "No query sent")
			}

			return methodData.Resource.ApiFind(methodData.Query, r)
		},
	}
}
