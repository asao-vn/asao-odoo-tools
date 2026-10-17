package secretpage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

var formToken = regexp.MustCompile(`name="token" value="([0-9a-f]+)"`)

// person stands in for the browser: it opens the page, and submits the form the way it is given.
func person(t *testing.T, submit func(address, token string)) func(string) error {
	return func(address string) error {
		go func() {
			response, err := http.Get(address)
			if err != nil {
				t.Error(err)
				return
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.Header.Get("Content-Security-Policy") == "" || !strings.Contains(string(body), `type="password"`) {
				t.Errorf("the page: %s %s", response.Header, body)
			}
			match := formToken.FindStringSubmatch(string(body))
			if match == nil {
				t.Error("the page carries no form token")
				return
			}
			submit(address, match[1])
		}()
		return nil
	}
}

func post(t *testing.T, address string, form url.Values, origin string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, address, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0 // the page has stopped: it took its one submission
	}
	response.Body.Close()
	return response.StatusCode
}

func TestCollectTakesOnlyTheFormOfItsOwnPageOnce(t *testing.T) {
	statuses := make(chan []int, 1)
	OpenBrowser = person(t, func(address, token string) {
		origin := strings.TrimSuffix(address, address[strings.LastIndex(address, "/"):])
		var got []int
		got = append(got, post(t, address, url.Values{"token": {"not-the-page's"}, "value": {"stolen"}}, origin))
		got = append(got, post(t, address, url.Values{"token": {token}, "value": {"stolen"}}, "https://evil.example"))
		got = append(got, post(t, address, url.Values{"token": {token}, "value": {"s3cr3t-key\n"}}, origin))
		got = append(got, post(t, address, url.Values{"token": {token}, "value": {"again"}}, origin))
		statuses <- got
	})
	t.Cleanup(func() { OpenBrowser = func(string) error { return nil } })
	announced := ""
	value, err := Collect(context.Background(), Request{Title: "API key for profile shop", Detail: "Odoo at https://erp.example.com",
		Timeout: 10 * time.Second, Announce: func(address string) { announced = address }})
	if err != nil {
		t.Fatal(err)
	}
	if value != "s3cr3t-key" {
		t.Fatalf("value %q", value)
	}
	if !strings.HasPrefix(announced, "http://127.0.0.1:") {
		t.Fatalf("announced %q", announced)
	}
	got := <-statuses
	if got[0] != http.StatusForbidden || got[1] != http.StatusForbidden || got[2] != http.StatusOK || (got[3] != 0 && got[3] != http.StatusGone) {
		t.Fatalf("statuses %v: another form, another origin, the page's own, then nothing more", got)
	}
}

func TestCollectGivesUpWhenThePersonDoesNotAnswer(t *testing.T) {
	OpenBrowser = func(string) error { return nil }
	_, err := Collect(context.Background(), Request{Title: "x", Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err %v", err)
	}
}

func TestThePageNeverQuotesWhatItIsGiven(t *testing.T) {
	html := page(Request{Title: `<script>alert(1)</script>`, Detail: `"quoted"`, Theme: "plain"}, "abc")
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("the title is not escaped: %s", html)
	}
}
