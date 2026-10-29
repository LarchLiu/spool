package workspace

import (
	"sort"
	"sync"
	"time"
)

type PresenceUser struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	State    string `json:"state"`
	Devices  int    `json:"devices"`
	Avatar   Avatar `json:"avatar"`
}
type presenceClient struct {
	User      User
	Visible   bool
	Seen      time.Time
	TokenHash string
}
type Presence struct {
	mu      sync.Mutex
	clients map[string]presenceClient
	changed func()
}

func (p *Presence) Touch(id authIdentity, client string, visible bool) {
	p.mu.Lock()
	changed := false
	defer func() {
		p.mu.Unlock()
		if changed && p.changed != nil {
			p.changed()
		}
	}()
	if p.clients == nil {
		p.clients = map[string]presenceClient{}
	}
	for key, value := range p.clients {
		if time.Since(value.Seen) > 90*time.Second {
			delete(p.clients, key)
			changed = true
		}
	}
	key := id.User.ID + ":" + client
	previous, exists := p.clients[key]
	if !exists && len(p.clients) >= 4096 {
		return
	}
	changed = changed || !exists || previous.Visible != visible || previous.TokenHash != id.TokenHash
	p.clients[key] = presenceClient{id.User, visible, time.Now(), id.TokenHash}
}

func (p *Presence) Disconnect(user, client string) {
	p.mu.Lock()
	key := user + ":" + client
	_, existed := p.clients[key]
	delete(p.clients, key)
	p.mu.Unlock()
	if existed && p.changed != nil {
		p.changed()
	}
}
func (p *Presence) Remove(token string) {
	p.mu.Lock()
	changed := false
	for key, value := range p.clients {
		if value.TokenHash == token {
			delete(p.clients, key)
			changed = true
		}
	}
	p.mu.Unlock()
	if changed && p.changed != nil {
		p.changed()
	}
}
func (p *Presence) List(store *Store) []PresenceUser {
	users, err := store.Users()
	if err != nil {
		return []PresenceUser{}
	}
	enabled := map[string]User{}
	for _, u := range users {
		if !u.Disabled {
			enabled[u.ID] = u
		}
	}
	validTokens := map[string]bool{}
	rows, err := store.db.Query(`SELECT token_hash FROM auth_sessions WHERE expires_at>?`, time.Now().UnixMilli())
	if err != nil {
		return []PresenceUser{}
	}
	for rows.Next() {
		var token string
		if rows.Scan(&token) == nil {
			validTokens[token] = true
		}
	}
	rows.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	byUser := map[string]*PresenceUser{}
	out := []PresenceUser{}
	for key, value := range p.clients {
		user, exists := enabled[value.User.ID]
		if time.Since(value.Seen) > 90*time.Second || !exists || !validTokens[value.TokenHash] {
			delete(p.clients, key)
			continue
		}
		v := byUser[value.User.ID]
		if v == nil {
			v = &PresenceUser{ID: user.ID, Nickname: user.Nickname, Avatar: user.Avatar, State: "away"}
			byUser[value.User.ID] = v
		}
		v.Devices++
		if value.Visible {
			v.State = "online"
		}
	}
	for _, v := range byUser {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nickname < out[j].Nickname })
	return out
}
