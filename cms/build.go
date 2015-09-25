package cms

import (
	db "github.com/theduke/go-dukedb"

	kit "github.com/theduke/go-appkit"
	"github.com/theduke/go-appkit/resources"
)

func Build(backend db.Backend, app kit.App, integerIds bool) {
	backend.RegisterModel(&Tag{})

	if integerIds {
		backend.RegisterModel(&AddressIntID{})

		backend.RegisterModel(&MenuIntID{})
		backend.RegisterModel(&MenuItemIntID{})
		backend.RegisterModel(&CommentIntID{})
		backend.RegisterModel(&PageIntID{})

		app.RegisterResource(resources.NewResource(&MenuIntID{}, MenuResource{}, true))
		app.RegisterResource(resources.NewResource(&MenuItemIntID{}, MenuItemResource{}, true))
		app.RegisterResource(resources.NewResource(&CommentIntID{}, CommentResource{}, true))
		app.RegisterResource(resources.NewResource(&PageIntID{}, PageResource{}, true))
	} else {
		backend.RegisterModel(&AddressStrID{})

		backend.RegisterModel(&MenuStrID{})
		backend.RegisterModel(&MenuItemStrID{})
		backend.RegisterModel(&CommentStrID{})
		backend.RegisterModel(&PageStrID{})

		app.RegisterResource(resources.NewResource(&MenuStrID{}, MenuResource{}, true))
		app.RegisterResource(resources.NewResource(&MenuItemStrID{}, MenuItemResource{}, true))
		app.RegisterResource(resources.NewResource(&CommentStrID{}, CommentResource{}, true))
		app.RegisterResource(resources.NewResource(&PageStrID{}, PageResource{}, true))
	}
}
