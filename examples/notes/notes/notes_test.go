package notes_test

import (
	"net/http"
	"testing"

	"github.com/mahrus-rohisyam/core-backend/apptest"
	"github.com/mahrus-rohisyam/core-backend/examples/notes/notes"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
)

func TestNotes(t *testing.T) {
	acc := accounts.New()
	srv := apptest.New(t, apptest.Options{Env: map[string]string{"APP_TENANCY": "multi"}}, acc, orgs.New(acc), notes.New())
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")
	org := func(c *apptest.Client) string {
		var list struct {
			Items []orgs.Org `json:"items"`
		}
		c.Get(t, "/api/v1/orgs").Expect(t, http.StatusOK).Decode(t, &list)
		return list.Items[0].ID
	}
	aliceNotes := "/api/v1/orgs/" + org(alice) + "/notes"

	alice.Post(t, aliceNotes, map[string]string{"title": " "}).Expect(t, http.StatusUnprocessableEntity)
	var n notes.Note
	alice.Post(t, aliceNotes, map[string]string{"title": "Groceries", "body": "milk"}).Expect(t, http.StatusCreated).Decode(t, &n)
	alice.Get(t, aliceNotes+"/"+n.ID).Expect(t, http.StatusOK)

	// Bob is in another organization: he can neither see Alice's notes nor reach them through his own.
	bob.Get(t, aliceNotes).Expect(t, http.StatusNotFound)
	bob.Get(t, aliceNotes+"/"+n.ID).Expect(t, http.StatusNotFound)
	bob.Get(t, "/api/v1/orgs/"+org(bob)+"/notes/"+n.ID).Expect(t, http.StatusNotFound)
	bob.Delete(t, "/api/v1/orgs/"+org(bob)+"/notes/"+n.ID).Expect(t, http.StatusNotFound)

	alice.Delete(t, aliceNotes+"/"+n.ID).Expect(t, http.StatusNoContent)
	alice.Get(t, aliceNotes+"/"+n.ID).Expect(t, http.StatusNotFound)
}
