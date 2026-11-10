package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

func TestParseNostrUsersURLs(t *testing.T) {
	got := ParseNostrUsersURLs(" https://a/x ,https://b/y\nhttps://a/x  ")
	want := []string{"https://a/x", "https://b/y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(ParseNostrUsersURLs("")) != 0 {
		t.Fatal("empty should parse to no URLs")
	}
}

func TestNostrUsersUnionAndKeepLastGood(t *testing.T) {
	var bDown atomic.Bool
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"names":{"x":"k1","y":"k2"}}`))
	}))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bDown.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"names":{"y":"k2","z":"k3"}}`))
	}))
	defer b.Close()

	var published []string
	s := NewNostrUsersService(a.URL+", "+b.URL, zap.NewNop(), func(k []string) { published = k })

	if missing := s.refresh(context.Background()); missing {
		t.Fatal("both sources should have loaded")
	}
	want := []string{"k1", "k2", "k3"}
	if !reflect.DeepEqual(published, want) {
		t.Fatalf("union: got %v want %v", published, want)
	}

	bDown.Store(true)
	if missing := s.refresh(context.Background()); missing {
		t.Fatal("b has a previous result, should not count as missing")
	}
	if !reflect.DeepEqual(s.GetCachedKeys(), want) {
		t.Fatalf("keep last good: got %v want %v", s.GetCachedKeys(), want)
	}

	// A source that never loaded is reported missing.
	s2 := NewNostrUsersService(a.URL+","+b.URL, zap.NewNop(), nil)
	if missing := s2.refresh(context.Background()); !missing {
		t.Fatal("b never loaded, should be missing")
	}
	if !reflect.DeepEqual(s2.GetCachedKeys(), []string{"k1", "k2"}) {
		t.Fatalf("partial: got %v", s2.GetCachedKeys())
	}

	// Removing a source drops its members.
	s.SetURL(a.URL)
	if !reflect.DeepEqual(s.GetCachedKeys(), []string{"k1", "k2"}) {
		t.Fatalf("after removal: got %v", s.GetCachedKeys())
	}
}
