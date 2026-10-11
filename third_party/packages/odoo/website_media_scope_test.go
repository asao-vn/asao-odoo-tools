package odoo

import (
	"context"
	"errors"
	"testing"
)

// website.media writes publish public files. Their provider domain holds the records they reach to
// /media/; InputPrefixes holds the url they write there too, so no write puts a public file at
// another path of the site.
func TestWebsiteMediaWritesStayUnderMedia(t *testing.T) {
	media := func(url string) *ExecutionIntent {
		return &ExecutionIntent{ID: "test", Operation: "website.media.create",
			Input: `{"values":{"name":"x","url":"` + url + `","datas":"eA=="}}`}
	}
	surface := func(tr Transport) *Adapter {
		a := NewAdapter(tr)
		a.Surface = true
		return a
	}
	for _, url := range []string{"/web/login", "/media", "/media/", "media/x.webm", "/media/../web/login", "/media//x.webm", "/media/./x.webm"} {
		tr := &fakeTransport{}
		_, err := surface(tr).Execute(context.Background(), media(url))
		var refused *PrefixValueError
		if !errors.As(err, &refused) || refused.ErrorClass() != string(ErrClassValidationFailed) || refused.Field != "url" {
			t.Fatalf("%s: want a VALIDATION_FAILED naming url, got %v", url, err)
		}
		assertNoMutation(t, tr, url)
	}
	for _, url := range []string{"/media/asao-hero.webm", "/media/fonts/ibm-plex-sans-400.woff2"} {
		if err := surface(&fakeTransport{}).ValidateInvocation(media(url)); err != nil {
			t.Fatalf("%s: a file under /media/ must be accepted, got %v", url, err)
		}
	}
}

// website.media.delete reaches only the media it publishes: a target outside /media/ (any other
// attachment, a user's file, an invoice's PDF) is refused before anything is deleted.
func TestWebsiteMediaDeleteStaysUnderMedia(t *testing.T) {
	remove := &ExecutionIntent{ID: "test", Operation: "website.media.delete", Input: `{"target_id":77}`}
	outside := &fakeTransport{}
	a := NewAdapter(outside)
	a.Surface = true
	if _, err := a.Execute(context.Background(), remove); err == nil {
		t.Fatal("an attachment outside /media/ was deleted")
	}
	assertNoMutation(t, outside, "delete outside /media/")

	inside := &fakeTransport{searchRead: func(model string, domain interface{}, _ []string) ([]map[string]interface{}, error) {
		return []map[string]interface{}{{"id": 77.0}}, nil
	}}
	a = NewAdapter(inside)
	a.Surface = true
	if _, err := a.Execute(context.Background(), remove); err != nil {
		t.Fatal(err)
	}
	if len(inside.mutations) != 1 || inside.mutations[0].Op != "Unlink" || inside.mutations[0].Model != "ir.attachment" {
		t.Fatalf("mutations %v: want the one unlink of the media", inside.mutations)
	}
}

// A media created without a url would fall outside /media/, where website.media can neither list,
// update nor delete it: the url is required on create (not on update) and the catalog says so.
func TestWebsiteMediaCreateRequiresItsURL(t *testing.T) {
	tr := &fakeTransport{}
	a := NewAdapter(tr)
	a.Surface = true
	_, err := a.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "website.media.create",
		Input: `{"values":{"name":"x","datas":"eA=="}}`})
	var missing *MissingFieldError
	if !errors.As(err, &missing) || len(missing.Missing) != 1 || missing.Missing[0] != "url" {
		t.Fatalf("want a MissingFieldError naming url, got %v", err)
	}
	assertNoMutation(t, tr, "media without a url")

	update := &ExecutionIntent{ID: "test", Operation: "website.media.update", Input: `{"target_id":77,"values":{"name":"renamed"}}`}
	if err := a.ValidateInvocation(update); err != nil {
		t.Fatalf("an update need not restate the url: %v", err)
	}
}
