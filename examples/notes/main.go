// Command notes is a small app built on the core: accounts, organizations, API tokens, and one
// feature module of its own. Copy this layout to start a new app.
//
//	export APP_PUBLIC_URL=http://localhost:8080 APP_DATABASE_URL=postgres://... APP_MASTER_KEY=$(openssl rand -base64 32)
//	go run ./examples/notes
package main

import (
	"github.com/mahrus-rohisyam/core-backend/app"
	"github.com/mahrus-rohisyam/core-backend/examples/notes/notes"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/apitokens"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
)

func main() {
	acc := accounts.New()
	app.Run(app.Options{Name: "Notes"},
		acc,
		orgs.New(acc),
		apitokens.New(acc),
		notes.New(),
	)
}
