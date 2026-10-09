package smtpfast

import (
	"context"
	"os"
	"testing"
)

// Read-only calls against the real API: each answer must decode into the
// generated types. Runs only with SMTPFAST_API_KEY set; a key with
// email:read and domain:read is enough.
func TestLive(t *testing.T) {
	if os.Getenv("SMTPFAST_API_KEY") == "" {
		t.Skip("SMTPFAST_API_KEY is not set")
	}
	c := NewClient()
	ctx := context.Background()
	check := func(name string, err error) {
		t.Helper()
		if IsForbidden(err) {
			t.Logf("%s: this key lacks the scope, not checked", name)
			return
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err := c.Account.Me(ctx)
	check("account.me", err)
	emails, err := c.Emails.List(ctx, &ListEmailsQuery{Limit: Int(3)})
	check("emails.list", err)
	if err == nil && len(emails.Data) > 0 {
		_, err = c.Emails.Get(ctx, emails.Data[0].ID)
		check("emails.get", err)
	}
	domains, err := c.Domains.List(ctx)
	check("domains.list", err)
	if err == nil && len(domains.Data) > 0 {
		_, err = c.Domains.Get(ctx, domains.Data[0].ID)
		check("domains.get", err)
	}
	_, err = c.Emails.GetMetrics(ctx, nil)
	check("emails.getMetrics", err)
	_, err = c.Emails.ListClickedLinks(ctx, nil)
	check("emails.listClickedLinks", err)
	_, err = c.Account.Usage(ctx)
	check("account.usage", err)
	_, err = c.Analytics.Get(ctx, nil)
	check("analytics.get", err)
	_, err = c.Logs.List(ctx, &ListLogsQuery{Limit: Int(3)})
	check("logs.list", err)
}
