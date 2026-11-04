package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFormatOnline(t *testing.T) {
	channels := []channel{{ID: "1", Name: "Общение"}, {ID: "2", Name: "Игры"}}
	clients := []client{{ChannelID: "1", Nickname: "Анна", Type: "0"}, {ChannelID: "1", Nickname: "Борис", Type: "0"}, {ChannelID: "2", Nickname: "Иван", Type: "0"}, {ChannelID: "2", Nickname: "Query", Type: "1"}, {ChannelID: "3", Nickname: "Неизвестный\nник", Type: "0"}}
	got := formatOnline(channels, clients)
	for _, expected := range []string{"Всего на сервере: 4", "Общение — 2 человека", "Игры — 1 человек", "Неизвестный канал (3) — 1 человек", "Неизвестный ник"} {
		if !strings.Contains(got, expected) {
			t.Errorf("missing %q in %q", expected, got)
		}
	}
	if strings.Contains(got, "Query") {
		t.Error("query connection included")
	}
}

func TestEmptyAndPlural(t *testing.T) {
	if !strings.Contains(formatOnline(nil, nil), "Всего на сервере: 0") {
		t.Fatal("wrong empty count")
	}
	for n, want := range map[int]string{1: "человек", 2: "человека", 5: "человек", 11: "человек", 21: "человек", 22: "человека", 112: "человек"} {
		if got := personWord(n); got != want {
			t.Errorf("%d: got %s, want %s", n, got, want)
		}
	}
}

func TestQueryFormatsAndError(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-api-key") != "secret" {
			t.Error("missing API key")
		}
		var body string
		switch r.URL.Path {
		case "/1/channellist":
			body = `{"status":{"code":0},"body":[{"cid":"12","channel_name":"Комната"}]}`
		case "/1/clientlist":
			body = `{"status":{"code":"0"},"body":[{"cid":12,"client_nickname":"Пользователь","client_type":0}]}`
		default:
			body = `{"status":{"code":2568},"body":[]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	c := config{queryURL: "http://teamspeak.test/1", key: "secret"}
	chs, err := query[channel](httpClient, c, "channellist")
	if err != nil || len(chs) != 1 || chs[0].ID != "12" {
		t.Fatalf("channel query: %v, %+v", err, chs)
	}
	clients, err := query[client](httpClient, c, "clientlist")
	if err != nil || len(clients) != 1 || clients[0].ChannelID != "12" || clients[0].Type != "0" {
		t.Fatalf("client query: %v, %+v", err, clients)
	}
	if _, err := query[client](httpClient, c, "invalid"); err == nil {
		t.Fatal("expected query error")
	}
}

func TestChunks(t *testing.T) {
	original := strings.Repeat("А", 8500)
	parts := chunks(original, 4000)
	if strings.Join(parts, "") != original {
		t.Fatal("data changed")
	}
	for _, p := range parts {
		if len([]rune(p)) > 4000 {
			t.Fatal("chunk too long")
		}
	}
}
