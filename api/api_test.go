package api_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mahrus-rohisyam/core-backend/api"
	"github.com/mahrus-rohisyam/core-backend/apptest"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/apitokens"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
)

// The spec and the router must list the same API routes.
func TestSpecMatchesRoutes(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	var documented []string
	for path, ops := range spec.Paths {
		for method := range ops {
			if method != "parameters" {
				documented = append(documented, strings.ToUpper(method)+" "+path)
			}
		}
	}

	acc := accounts.New()
	srv := apptest.New(t, apptest.Options{}, acc, orgs.New(acc), apitokens.New(acc))
	var served []string
	for _, r := range srv.App.Routes() {
		if strings.Contains(r, " /api/") {
			served = append(served, r)
		}
	}
	for _, r := range served {
		if !slices.Contains(documented, r) {
			t.Errorf("served but not in openapi.json: %s", r)
		}
	}
	for _, r := range documented {
		if !slices.Contains(served, r) {
			t.Errorf("in openapi.json but not served: %s", r)
		}
	}
}
