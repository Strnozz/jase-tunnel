package main

import (
	"sort"
	"strings"
	"time"
)

// One entry of the panel's site list: the site, its tunnel and its console.
type desktopSite struct {
	Origin    string    `json:"origin"`
	Label     string    `json:"label"`
	State     string    `json:"state"`
	Status    string    `json:"status"`
	Model     string    `json:"model,omitempty"`
	ExpiresAt int64     `json:"expiresAt,omitempty"`
	Active    bool      `json:"active"`
	Events    int       `json:"events"`
	last      time.Time // latest journal event, for ordering
}

// The most relevant state of a site's recent discoveries, highest first.
var siteStatePriority = map[string]int{"awaiting_approval": 7, "connecting": 6, "requested": 5, "compatible": 4, "verifying": 3, "failed": 2, "rejected": 2, "incompatible": 1}

func siteLabel(origin string) string { return strings.TrimPrefix(origin, "https://") }

func siteStatus(s desktopSite) string {
	switch s.State {
	case "open":
		// The list marks open tunnels already: the line keeps what fits in the column.
		text := s.Model + " · fino alle " + time.UnixMilli(s.ExpiresAt).Format("15:04")
		if s.Active {
			text += " · attivo"
		}
		return text
	case "awaiting_approval":
		return "Chiede il tunnel: conferma nel pannello"
	case "connecting":
		return "Collegamento in corso"
	case "requested":
		return "Collegamento richiesto dalla notifica"
	case "compatible":
		return "Pagina compatibile · nessun tunnel aperto"
	case "verifying":
		return "Verifica del sito in corso"
	case "incompatible":
		return "Sito non compatibile"
	case "rejected":
		return "Tunnel rifiutato o chiuso"
	case "failed":
		return "Collegamento non riuscito"
	}
	return "Nessun tunnel aperto"
}

// desktopSites lists every site with an open tunnel, a pending request or events in
// the journal: open tunnels first, then requests waiting for the person, then the rest
// by latest activity. Each lock is taken on its own, never nested.
func (a *application) desktopSites() []desktopSite {
	now := time.Now()
	sites := map[string]*desktopSite{}
	get := func(origin string) *desktopSite {
		if sites[origin] == nil {
			sites[origin] = &desktopSite{Origin: origin, Label: siteLabel(origin)}
		}
		return sites[origin]
	}
	a.mu.RLock()
	for _, c := range a.settings.Links {
		if c.ExpiresAt <= now.UnixMilli() {
			continue
		}
		s := get(c.Origin)
		s.State = "open"
		if s.Model == "" {
			s.Model = c.Model.Name
		} else if !strings.Contains(s.Model, c.Model.Name) {
			s.Model += ", " + c.Model.Name
		}
		if c.ExpiresAt > s.ExpiresAt {
			s.ExpiresAt = c.ExpiresAt
		}
	}
	a.mu.RUnlock()
	m := a.webManager()
	m.mu.Lock()
	for _, d := range m.requests {
		if d.ExpiresAt <= now.UnixMilli() {
			continue
		}
		s := get(d.Origin)
		if s.State != "open" && siteStatePriority[d.State] > siteStatePriority[s.State] {
			s.State = d.State
		}
	}
	m.mu.Unlock()
	a.activity.mu.Lock()
	for _, e := range a.activity.events {
		if e.Site == "" {
			continue
		}
		s := get(e.Site)
		s.Events++
		s.last = e.Time
	}
	for origin, s := range sites {
		if seen := a.activity.contacts[origin]; !seen.IsZero() && now.Sub(seen) < 90*time.Second {
			s.Active = s.State == "open"
		}
	}
	a.activity.mu.Unlock()
	list := []desktopSite{}
	for _, s := range sites {
		s.Status = siteStatus(*s)
		list = append(list, *s)
	}
	rank := func(s desktopSite) int {
		switch {
		case s.State == "open":
			return 3
		case s.State == "awaiting_approval" || s.State == "requested" || s.State == "connecting":
			return 2
		}
		return 1
	}
	sort.SliceStable(list, func(i, j int) bool {
		if rank(list[i]) != rank(list[j]) {
			return rank(list[i]) > rank(list[j])
		}
		if !list[i].last.Equal(list[j].last) {
			return list[i].last.After(list[j].last)
		}
		return list[i].Origin < list[j].Origin
	})
	return list
}
