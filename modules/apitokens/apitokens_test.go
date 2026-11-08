package apitokens_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mahrus-rohisyam/core-backend/apptest"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/apitokens"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
)

func TestAPITokens(t *testing.T) {
	acc := accounts.New()
	srv := apptest.New(t, apptest.Options{}, acc, orgs.New(acc), apitokens.New(acc))
	alice := srv.SignUp(t, "alice@example.com")
	bob := srv.SignUp(t, "bob@example.com")
	var list struct {
		Items []orgs.Org `json:"items"`
	}
	alice.Get(t, "/api/v1/orgs").Expect(t, http.StatusOK).Decode(t, &list)
	base := "/api/v1/orgs/" + list.Items[0].ID

	// Bob is a member: he cannot create a token with permissions he lacks.
	bob.Post(t, base+"/api-tokens", map[string]any{"name": "ci", "permissions": []string{"audit.read"}}).Expect(t, http.StatusUnprocessableEntity)
	bob.Post(t, base+"/api-tokens", map[string]any{"name": "ci", "permissions": []string{}}).Expect(t, http.StatusUnprocessableEntity)

	var tok apitokens.Token
	alice.Post(t, base+"/api-tokens", map[string]any{"name": "ci", "permissions": []string{"members.read", "audit.read"}, "expires_in_days": 30}).
		Expect(t, http.StatusCreated).Decode(t, &tok)
	if !strings.HasPrefix(tok.Token, apitokens.Prefix) || tok.ExpiresAt == nil || !strings.HasPrefix(tok.Token, tok.Prefix) {
		t.Fatalf("token = %+v", tok)
	}

	api := srv.Token(tok.Token)
	api.Get(t, base+"/members").Expect(t, http.StatusOK)
	api.Get(t, base+"/audit-events").Expect(t, http.StatusOK)
	// Granted to Alice but not to the token.
	api.Get(t, base+"/invitations").Expect(t, http.StatusForbidden)
	// Tokens cannot change account settings or make more tokens, and need no CSRF token.
	api.Get(t, "/api/v1/account/sessions").Expect(t, http.StatusForbidden)
	api.Post(t, base+"/api-tokens", map[string]any{"name": "x", "permissions": []string{"members.read"}}).Expect(t, http.StatusForbidden)
	api.Get(t, "/api/v1/session").Expect(t, http.StatusOK)

	srv.Token(apitokens.Prefix+"invalid").Get(t, base+"/members").Expect(t, http.StatusUnauthorized)

	// The listing hides the secret; members only see their own tokens.
	var tokens struct {
		Items []apitokens.Token `json:"items"`
	}
	alice.Get(t, base+"/api-tokens").Expect(t, http.StatusOK).Decode(t, &tokens)
	if len(tokens.Items) != 1 || tokens.Items[0].Token != "" || tokens.Items[0].LastUsedAt == nil {
		t.Fatalf("tokens = %+v", tokens.Items)
	}
	bob.Get(t, base+"/api-tokens").Expect(t, http.StatusOK).Decode(t, &tokens)
	if len(tokens.Items) != 0 {
		t.Fatal("member sees another member's token")
	}
	bob.Delete(t, base+"/api-tokens/"+tok.ID).Expect(t, http.StatusNotFound)

	// Lowering the creator's role lowers the token too.
	var members struct {
		Items []orgs.Member `json:"items"`
	}
	alice.Get(t, base+"/members").Expect(t, http.StatusOK).Decode(t, &members)
	i := slices.IndexFunc(members.Items, func(m orgs.Member) bool { return m.Email == "bob@example.com" })
	alice.Patch(t, base+"/members/"+members.Items[i].UserID, map[string]string{"role_id": orgs.RoleAdmin}).Expect(t, http.StatusNoContent)
	var bobTok apitokens.Token
	bob.Post(t, base+"/api-tokens", map[string]any{"name": "bob", "permissions": []string{"audit.read"}}).Expect(t, http.StatusCreated).Decode(t, &bobTok)
	srv.Token(bobTok.Token).Get(t, base+"/audit-events").Expect(t, http.StatusOK)
	alice.Patch(t, base+"/members/"+members.Items[i].UserID, map[string]string{"role_id": orgs.RoleMember}).Expect(t, http.StatusNoContent)
	srv.Token(bobTok.Token).Get(t, base+"/audit-events").Expect(t, http.StatusForbidden)

	alice.Delete(t, base+"/api-tokens/"+tok.ID).Expect(t, http.StatusNoContent)
	api.Get(t, base+"/members").Expect(t, http.StatusUnauthorized)
}

func TestPasswordResetRevokesTokens(t *testing.T) {
	acc := accounts.New()
	srv := apptest.New(t, apptest.Options{}, acc, orgs.New(acc), apitokens.New(acc))
	alice := srv.SignUp(t, "alice@example.com")
	var list struct {
		Items []orgs.Org `json:"items"`
	}
	alice.Get(t, "/api/v1/orgs").Expect(t, http.StatusOK).Decode(t, &list)
	base := "/api/v1/orgs/" + list.Items[0].ID
	var tok apitokens.Token
	alice.Post(t, base+"/api-tokens", map[string]any{"name": "ci", "permissions": []string{"members.read"}}).
		Expect(t, http.StatusCreated).Decode(t, &tok)
	srv.Token(tok.Token).Get(t, base+"/members").Expect(t, http.StatusOK)

	srv.Client().Post(t, "/api/v1/auth/password-reset", map[string]string{"email": "alice@example.com"}).Expect(t, http.StatusAccepted)
	srv.Client().Post(t, "/api/v1/auth/password-reset/confirm", map[string]string{"token": srv.LinkToken(t, "alice@example.com"), "password": "a whole new passphrase"}).
		Expect(t, http.StatusNoContent)
	srv.Token(tok.Token).Get(t, base+"/members").Expect(t, http.StatusUnauthorized)
}
