package orgs_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mahrus-rohisyam/core-backend/apptest"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/apitokens"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
)

func start(t *testing.T, env map[string]string) *apptest.Server {
	t.Helper()
	acc := accounts.New()
	return apptest.New(t, apptest.Options{Env: env}, acc, orgs.New(acc), apitokens.New(acc))
}

func multi() map[string]string { return map[string]string{"APP_TENANCY": "multi"} }

func firstOrg(t *testing.T, c *apptest.Client) orgs.Org {
	t.Helper()
	var list struct {
		Items []orgs.Org `json:"items"`
	}
	c.Get(t, "/api/v1/orgs").Expect(t, http.StatusOK).Decode(t, &list)
	if len(list.Items) == 0 {
		t.Fatal("no organizations")
	}
	return list.Items[0]
}

func TestSingleTenancy(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")

	a, b := firstOrg(t, alice), firstOrg(t, bob)
	if a.ID != b.ID || a.Role != "owner" || b.Role != "member" {
		t.Fatalf("alice %+v, bob %+v", a, b)
	}
	bob.Post(t, "/api/v1/orgs", map[string]string{"name": "Second"}).Expect(t, http.StatusForbidden)
	bob.Patch(t, "/api/v1/orgs/"+a.ID, map[string]string{"name": "Renamed"}).Expect(t, http.StatusForbidden)
	bob.Get(t, "/api/v1/orgs/"+a.ID+"/members").Expect(t, http.StatusOK)
	bob.Get(t, "/api/v1/orgs/"+a.ID+"/audit-events").Expect(t, http.StatusForbidden)
	alice.Patch(t, "/api/v1/orgs/"+a.ID, map[string]string{"name": "Renamed"}).Expect(t, http.StatusOK)
}

func TestMultiTenancy(t *testing.T) {
	srv := start(t, multi())
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")
	if firstOrg(t, alice).ID == firstOrg(t, bob).ID {
		t.Fatal("users share an organization in multi mode")
	}
	var o orgs.Org
	alice.Post(t, "/api/v1/orgs", map[string]string{"name": "Side project"}).Expect(t, http.StatusCreated).Decode(t, &o)
	if o.Role != "owner" || !slices.Contains(o.Permissions, orgs.PermMembersManage) {
		t.Fatalf("created org = %+v", o)
	}
	alice.Post(t, "/api/v1/orgs", map[string]string{"name": ""}).Expect(t, http.StatusUnprocessableEntity)
}

// Every route under an organization answers 404 to someone outside it, whatever the method.
func TestCrossOrganizationAccess(t *testing.T) {
	srv := start(t, multi())
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")
	bobOrg := firstOrg(t, bob).ID

	var token struct {
		Token string `json:"token"`
	}
	aliceOrg := firstOrg(t, alice).ID
	alice.Post(t, "/api/v1/orgs/"+aliceOrg+"/api-tokens", map[string]any{"name": "ci", "permissions": []string{"members.read"}}).
		Expect(t, http.StatusCreated).Decode(t, &token)
	aliceToken := srv.Token(token.Token)

	n := 0
	for _, route := range srv.App.Routes() {
		method, path, _ := strings.Cut(route, " ")
		if !strings.Contains(path, "{org}") {
			continue
		}
		n++
		path = strings.ReplaceAll(path, "{org}", bobOrg)
		for _, name := range []string{"{user}", "{role}", "{id}"} {
			path = strings.ReplaceAll(path, name, id.New())
		}
		var body any
		if method != http.MethodGet && method != http.MethodDelete {
			body = map[string]any{}
		}
		if resp := alice.Do(t, method, path, body, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("session %s %s: status %d, want 404", method, path, resp.StatusCode)
		}
		if resp := aliceToken.Do(t, method, path, body, nil); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
			t.Errorf("token %s %s: status %d, want 404 or 403", method, path, resp.StatusCode)
		}
	}
	if n < 10 {
		t.Fatalf("only %d organization routes checked", n)
	}
	// Bob's org does not appear in Alice's list.
	var list struct {
		Items []orgs.Org `json:"items"`
	}
	alice.Get(t, "/api/v1/orgs").Expect(t, http.StatusOK).Decode(t, &list)
	for _, o := range list.Items {
		if o.ID == bobOrg {
			t.Fatal("other organization listed")
		}
	}
}

// A record from another organization is not found through your own organization's URLs.
func TestForeignRecordsInOwnOrganization(t *testing.T) {
	srv := start(t, multi())
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")
	a := "/api/v1/orgs/" + firstOrg(t, alice).ID
	b := "/api/v1/orgs/" + firstOrg(t, bob).ID

	var role, inv, tok struct {
		ID string `json:"id"`
	}
	bob.Post(t, b+"/roles", map[string]any{"name": "Bob's role", "permissions": []string{"members.read"}}).
		Expect(t, http.StatusCreated).Decode(t, &role)
	bob.Post(t, b+"/invitations", map[string]string{"email": "carol@example.com", "role_id": orgs.RoleMember}).
		Expect(t, http.StatusCreated).Decode(t, &inv)
	bob.Post(t, b+"/api-tokens", map[string]any{"name": "ci", "permissions": []string{"members.read"}}).
		Expect(t, http.StatusCreated).Decode(t, &tok)
	var members struct {
		Items []orgs.Member `json:"items"`
	}
	bob.Get(t, b+"/members").Expect(t, http.StatusOK).Decode(t, &members)
	bobID := members.Items[0].UserID

	alice.Patch(t, a+"/roles/"+role.ID, map[string]any{"name": "Taken", "permissions": []string{"members.read"}}).Expect(t, http.StatusNotFound)
	alice.Delete(t, a+"/roles/"+role.ID).Expect(t, http.StatusNotFound)
	alice.Delete(t, a+"/invitations/"+inv.ID).Expect(t, http.StatusNotFound)
	alice.Delete(t, a+"/api-tokens/"+tok.ID).Expect(t, http.StatusNotFound)
	alice.Patch(t, a+"/members/"+bobID, map[string]string{"role_id": orgs.RoleMember}).Expect(t, http.StatusNotFound)
	alice.Delete(t, a+"/members/"+bobID).Expect(t, http.StatusNotFound)
	alice.Post(t, a+"/invitations", map[string]string{"email": "dave@example.com", "role_id": role.ID}).Expect(t, http.StatusUnprocessableEntity)

	// Bob's records are untouched.
	bob.Delete(t, b+"/roles/"+role.ID).Expect(t, http.StatusNoContent)
	bob.Delete(t, b+"/invitations/"+inv.ID).Expect(t, http.StatusNoContent)
	bob.Delete(t, b+"/api-tokens/"+tok.ID).Expect(t, http.StatusNoContent)
}

// An invitation stops working when the person who sent it can no longer grant the role.
func TestInvitationFromDemotedMember(t *testing.T) {
	srv := start(t, map[string]string{"APP_SIGNUP": "invite"})
	alice := srv.SignUp(t, "alice@example.com")
	base := "/api/v1/orgs/" + firstOrg(t, alice).ID
	alice.Post(t, base+"/invitations", map[string]string{"email": "bob@example.com", "role_id": orgs.RoleAdmin}).Expect(t, http.StatusCreated)
	bob := srv.SignUpWith(t, map[string]any{"email": "bob@example.com", "name": "Bob", "password": apptest.Password, "invitation": srv.LinkToken(t, "bob@example.com")})

	bob.Post(t, base+"/invitations", map[string]string{"email": "carol@example.com", "role_id": orgs.RoleAdmin}).Expect(t, http.StatusCreated)
	bob.Post(t, base+"/invitations", map[string]string{"email": "dave@example.com", "role_id": orgs.RoleMember}).Expect(t, http.StatusCreated)
	carolToken, daveToken := srv.LinkToken(t, "carol@example.com"), srv.LinkToken(t, "dave@example.com")

	var members struct {
		Items []orgs.Member `json:"items"`
	}
	alice.Get(t, base+"/members").Expect(t, http.StatusOK).Decode(t, &members)
	i := slices.IndexFunc(members.Items, func(m orgs.Member) bool { return m.Email == "bob@example.com" })
	alice.Patch(t, base+"/members/"+members.Items[i].UserID, map[string]string{"role_id": orgs.RoleMember}).Expect(t, http.StatusNoContent)

	srv.Client().Post(t, "/api/v1/auth/sign-up", map[string]any{"email": "carol@example.com", "name": "Carol", "password": apptest.Password, "invitation": carolToken}).
		Expect(t, http.StatusBadRequest)

	// Removed entirely: no invitation of theirs works.
	alice.Delete(t, base+"/members/"+members.Items[i].UserID).Expect(t, http.StatusNoContent)
	srv.Client().Post(t, "/api/v1/auth/sign-up", map[string]any{"email": "dave@example.com", "name": "Dave", "password": apptest.Password, "invitation": daveToken}).
		Expect(t, http.StatusBadRequest)
}

func TestInvitations(t *testing.T) {
	srv := start(t, map[string]string{"APP_TENANCY": "multi", "APP_SIGNUP": "invite"})
	alice := srv.SignUp(t, "alice@example.com")
	org := firstOrg(t, alice).ID
	base := "/api/v1/orgs/" + org

	alice.Post(t, base+"/invitations", map[string]string{"email": "carol@example.com", "role_id": "00000000-0000-7000-8000-00000000ffff"}).
		Expect(t, http.StatusUnprocessableEntity)
	var inv orgs.Invitation
	alice.Post(t, base+"/invitations", map[string]string{"email": "Carol@Example.com", "role_id": orgs.RoleAdmin}).
		Expect(t, http.StatusCreated).Decode(t, &inv)
	token := srv.LinkToken(t, "carol@example.com")

	var lookup struct {
		OrgName string `json:"org_name"`
		Email   string `json:"email"`
	}
	srv.Client().Post(t, "/api/v1/invitations/lookup", map[string]string{"token": token}).Expect(t, http.StatusOK).Decode(t, &lookup)
	if lookup.Email != "carol@example.com" {
		t.Fatalf("lookup = %+v", lookup)
	}

	// The token only works for the invited address.
	srv.Client().Post(t, "/api/v1/auth/sign-up", map[string]string{"email": "mallory@example.com", "name": "M", "password": apptest.Password, "invitation": token}).
		Expect(t, http.StatusBadRequest)
	srv.Client().Post(t, "/api/v1/auth/sign-up", map[string]string{"email": "mallory@example.com", "name": "M", "password": apptest.Password, "invitation": "made-up"}).
		Expect(t, http.StatusBadRequest)

	carol := srv.SignUpWith(t, map[string]any{"email": "carol@example.com", "name": "Carol", "password": apptest.Password, "invitation": token})
	o := firstOrg(t, carol)
	if o.ID != org || o.Role != "admin" {
		t.Fatalf("carol joined %+v", o)
	}
	srv.Client().Post(t, "/api/v1/invitations/lookup", map[string]string{"token": token}).Expect(t, http.StatusBadRequest)

	// An existing member cannot be invited again; a signed-in user accepts with the token.
	alice.Post(t, base+"/invitations", map[string]string{"email": "carol@example.com", "role_id": orgs.RoleMember}).Expect(t, http.StatusConflict)
	alice.Post(t, base+"/invitations", map[string]string{"email": "dave@example.com", "role_id": orgs.RoleMember}).Expect(t, http.StatusCreated)
	var pending struct {
		Items []orgs.Invitation `json:"items"`
	}
	alice.Get(t, base+"/invitations").Expect(t, http.StatusOK).Decode(t, &pending)
	if len(pending.Items) != 1 || pending.Items[0].Email != "dave@example.com" {
		t.Fatalf("pending = %+v", pending.Items)
	}
	alice.Delete(t, base+"/invitations/"+pending.Items[0].ID).Expect(t, http.StatusNoContent)
	srv.Client().Post(t, "/api/v1/invitations/lookup", map[string]string{"token": srv.LinkToken(t, "dave@example.com")}).
		Expect(t, http.StatusBadRequest)
}

func TestRolesAndMembers(t *testing.T) {
	srv := start(t, nil)
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")
	carol := srv.SignUp(t, "carol@example.com")
	org := firstOrg(t, alice).ID
	base := "/api/v1/orgs/" + org

	var members struct {
		Items []orgs.Member `json:"items"`
		Next  string        `json:"next"`
	}
	alice.Get(t, base+"/members").Expect(t, http.StatusOK).Decode(t, &members)
	if len(members.Items) != 3 {
		t.Fatalf("got %d members", len(members.Items))
	}
	ids := map[string]string{}
	for _, m := range members.Items {
		ids[m.Email] = m.UserID
	}
	alice.Get(t, base+"/members?limit=2").Expect(t, http.StatusOK).Decode(t, &members)
	if len(members.Items) != 2 || members.Next == "" {
		t.Fatalf("page = %+v", members)
	}
	alice.Get(t, base+"/members?limit=2&after="+members.Next).Expect(t, http.StatusOK).Decode(t, &members)
	if len(members.Items) != 1 {
		t.Fatalf("second page has %d", len(members.Items))
	}

	// A custom role that can manage members.
	var role struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
	}
	alice.Post(t, base+"/roles", map[string]any{"name": "Manager", "permissions": []string{"members.read", "members.manage", "nope.read"}}).
		Expect(t, http.StatusUnprocessableEntity)
	alice.Post(t, base+"/roles", map[string]any{"name": "Manager", "permissions": []string{"members.read", "members.manage"}}).
		Expect(t, http.StatusCreated).Decode(t, &role)
	bob.Post(t, base+"/roles", map[string]any{"name": "Sneaky", "permissions": []string{"members.read"}}).Expect(t, http.StatusForbidden)

	alice.Patch(t, base+"/members/"+ids["bob@example.com"], map[string]string{"role_id": role.ID}).Expect(t, http.StatusNoContent)
	var bobOrg orgs.Org
	bob.Get(t, base).Expect(t, http.StatusOK).Decode(t, &bobOrg)
	if bobOrg.Role != "custom" || !slices.Equal(bobOrg.Permissions, []string{"members.read", "members.manage"}) {
		t.Fatalf("bob = %+v", bobOrg)
	}

	// Bob may manage members but cannot hand out more than he has, or touch the owner.
	bob.Patch(t, base+"/members/"+ids["carol@example.com"], map[string]string{"role_id": orgs.RoleAdmin}).Expect(t, http.StatusForbidden)
	bob.Patch(t, base+"/members/"+ids["alice@example.com"], map[string]string{"role_id": orgs.RoleMember}).Expect(t, http.StatusForbidden)
	bob.Patch(t, base+"/members/"+ids["carol@example.com"], map[string]string{"role_id": role.ID}).Expect(t, http.StatusNoContent)
	bob.Delete(t, base+"/members/"+ids["alice@example.com"]).Expect(t, http.StatusForbidden)

	alice.Delete(t, base+"/roles/"+role.ID).Expect(t, http.StatusConflict)
	alice.Delete(t, base+"/roles/"+orgs.RoleAdmin).Expect(t, http.StatusForbidden)
	alice.Patch(t, base+"/roles/"+role.ID, map[string]any{"name": "Reader", "permissions": []string{"members.read"}}).Expect(t, http.StatusOK)
	bob.Patch(t, base+"/members/"+ids["carol@example.com"], map[string]string{"role_id": orgs.RoleMember}).Expect(t, http.StatusForbidden)

	// The last owner cannot leave or be demoted; a member can leave.
	alice.Delete(t, base+"/members/"+ids["alice@example.com"]).Expect(t, http.StatusConflict)
	alice.Patch(t, base+"/members/"+ids["alice@example.com"], map[string]string{"role_id": orgs.RoleAdmin}).Expect(t, http.StatusConflict)
	carol.Delete(t, base+"/members/"+ids["carol@example.com"]).Expect(t, http.StatusNoContent)
	carol.Get(t, base).Expect(t, http.StatusNotFound)

	var events struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	alice.Get(t, base+"/audit-events").Expect(t, http.StatusOK).Decode(t, &events)
	var actions []string
	for _, e := range events.Items {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{"role.created", "member.role_changed", "member.left"} {
		if !slices.Contains(actions, want) {
			t.Errorf("audit log has no %s: %v", want, actions)
		}
	}
}
