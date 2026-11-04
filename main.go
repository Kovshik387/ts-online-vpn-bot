package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// TeamSpeak can return IDs and numeric fields either as JSON strings or numbers.
type scalar string

func (s *scalar) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*s = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = scalar(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = scalar(n.String())
	return nil
}

type channel struct {
	ID   scalar `json:"cid"`
	Name string `json:"channel_name"`
}
type client struct {
	ChannelID scalar `json:"cid"`
	Nickname  string `json:"client_nickname"`
	Type      scalar `json:"client_type"`
}
type queryResponse[T any] struct {
	Status struct {
		Code    scalar `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	Body []T `json:"body"`
}

type config struct {
	token, queryURL, key string
	allowed              map[int64]bool
}

func loadConfig() (config, error) {
	c := config{token: strings.TrimSpace(os.Getenv("TS6_BOT_TOKEN")), queryURL: strings.TrimRight(strings.TrimSpace(os.Getenv("TS6_WEBQUERY_URL")), "/"), key: strings.TrimSpace(os.Getenv("TS6_WEBQUERY_KEY")), allowed: make(map[int64]bool)}
	if c.queryURL == "" {
		c.queryURL = "http://127.0.0.1:10080/1"
	}
	u, err := url.Parse(c.queryURL)
	if c.token == "" || c.key == "" {
		return c, errors.New("set TS6_BOT_TOKEN and TS6_WEBQUERY_KEY")
	}
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("invalid TS6_WEBQUERY_URL")
	}
	for _, part := range strings.Split(os.Getenv("TS6_ALLOWED_CHAT_IDS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return c, errors.New("invalid TS6_ALLOWED_CHAT_IDS")
		}
		c.allowed[id] = true
	}
	return c, nil
}

func query[T any](httpClient *http.Client, c config, command string) ([]T, error) {
	req, err := http.NewRequest(http.MethodGet, c.queryURL+"/"+command, nil)
	if err != nil {
		return nil, errors.New("cannot create TeamSpeak request")
	}
	req.Header.Set("x-api-key", c.key)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TeamSpeak %s: connection failed", command)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TeamSpeak %s: HTTP %d", command, resp.StatusCode)
	}
	var data queryResponse[T]
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&data); err != nil {
		return nil, fmt.Errorf("TeamSpeak %s: invalid response", command)
	}
	if data.Status.Code != "0" {
		return nil, fmt.Errorf("TeamSpeak %s: query error code %s", command, data.Status.Code)
	}
	return data.Body, nil
}

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func personWord(n int) string {
	if n%100 >= 11 && n%100 <= 14 {
		return "человек"
	}
	switch n % 10 {
	case 1:
		return "человек"
	case 2, 3, 4:
		return "человека"
	default:
		return "человек"
	}
}

func formatOnline(channels []channel, clients []client) string {
	names := make(map[scalar]string)
	for _, c := range channels {
		name := clean(c.Name)
		if name == "" {
			name = "Без названия"
		}
		names[c.ID] = name
	}
	groups := make(map[scalar][]string)
	total := 0
	for _, c := range clients {
		if c.Type != "0" {
			continue
		}
		nickname := clean(c.Nickname)
		if nickname == "" {
			nickname = "Без имени"
		}
		groups[c.ChannelID] = append(groups[c.ChannelID], nickname)
		total++
	}
	var out strings.Builder
	fmt.Fprintf(&out, "👥 Всего на сервере: %d", total)
	if total == 0 {
		out.WriteString("\n\nСейчас никого нет.")
		return out.String()
	}
	ids := make([]scalar, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := names[ids[i]], names[ids[j]]
		if a == b {
			return ids[i] < ids[j]
		}
		return a < b
	})
	for _, id := range ids {
		name, ok := names[id]
		if !ok {
			name = "Неизвестный канал (" + clean(string(id)) + ")"
		}
		people := groups[id]
		sort.Strings(people)
		fmt.Fprintf(&out, "\n\n🎧 %s — %d %s", name, len(people), personWord(len(people)))
		for _, person := range people {
			fmt.Fprintf(&out, "\n  • %s", person)
		}
	}
	return out.String()
}

// Telegram limits text to 4096 characters. Split on rune boundaries.
func chunks(s string, max int) []string {
	r := []rune(s)
	var result []string
	for len(r) > max {
		cut := max
		for i := max - 1; i > max/2; i-- {
			if r[i] == '\n' {
				cut = i + 1
				break
			}
		}
		result = append(result, string(r[:cut]))
		r = r[cut:]
	}
	if len(r) > 0 {
		result = append(result, string(r))
	}
	return result
}

type telegram struct {
	token string
	http  *http.Client
}
type telegramResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (t telegram) call(method string, payload any, result any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return errors.New("cannot encode Telegram request")
	}
	resp, err := t.http.Post("https://api.telegram.org/bot"+t.token+"/"+method, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("Telegram %s: connection failed", method)
	}
	defer resp.Body.Close()
	var data telegramResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&data); err != nil {
		return fmt.Errorf("Telegram %s: invalid response", method)
	}
	if !data.OK {
		return fmt.Errorf("Telegram %s: HTTP %d, API rejected request", method, resp.StatusCode)
	}
	if result != nil {
		if err := json.Unmarshal(data.Result, result); err != nil {
			return fmt.Errorf("Telegram %s: invalid result", method)
		}
	}
	return nil
}
func (t telegram) send(chatID int64, message string) error {
	for _, part := range chunks(message, 4000) {
		if err := t.call("sendMessage", map[string]any{"chat_id": chatID, "text": part, "disable_notification": true, "reply_markup": map[string]bool{"remove_keyboard": true}}, nil); err != nil {
			return err
		}
	}
	return nil
}

type update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 40 * time.Second}
	tg := telegram{c.token, httpClient}
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		if err := tg.call("getMe", map[string]any{}, nil); err != nil {
			log.Fatal(err)
		}
		var hook struct {
			URL string `json:"url"`
		}
		if err := tg.call("getWebhookInfo", map[string]any{}, &hook); err != nil {
			log.Fatal(err)
		}
		if hook.URL != "" {
			log.Fatal("Telegram webhook is configured; long polling cannot run")
		}
		if _, err := query[channel](httpClient, c, "channellist"); err != nil {
			log.Fatal(err)
		}
		if _, err := query[client](httpClient, c, "clientlist"); err != nil {
			log.Fatal(err)
		}
		log.Print("Telegram and TeamSpeak are reachable")
		return
	}
	log.Print("bot started")
	var offset int64
	for {
		var updates []update
		if err := tg.call("getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &updates); err != nil {
			log.Print(err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, u := range updates {
			if u.ID >= offset {
				offset = u.ID + 1
			}
			if u.Message == nil {
				continue
			}
			id := u.Message.Chat.ID
			if len(c.allowed) > 0 && !c.allowed[id] {
				continue
			}
			fields := strings.Fields(u.Message.Text)
			if len(fields) == 0 {
				continue
			}
			cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
			if cmd != "/start" && cmd != "/online" {
				continue
			}
			chs, err := query[channel](httpClient, c, "channellist")
			if err == nil {
				var clients []client
				clients, err = query[client](httpClient, c, "clientlist")
				if err == nil {
					err = tg.send(id, formatOnline(chs, clients))
					if err != nil {
						log.Print(err)
					}
					continue
				}
			}
			log.Print(err)
			if err := tg.send(id, "Не удалось получить список пользователей TeamSpeak. Попробуйте позже."); err != nil {
				log.Print(err)
			}
		}
	}
}
