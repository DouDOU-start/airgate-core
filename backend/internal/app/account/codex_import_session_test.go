package account

import "testing"

func TestParseCodexSessionJSONNormalizesSnakeCaseBeforeValidation(t *testing.T) {
	session, err := parseCodexSessionJSON(`{"access_token":"access","session_token":"session","account_id":"acct"}`)
	if err != nil {
		t.Fatalf("parseCodexSessionJSON returned error: %v", err)
	}
	if session.AccessToken != "access" || session.SessionToken != "session" || session.Account.ID != "acct" {
		t.Fatalf("normalized session = %#v", session)
	}
}
