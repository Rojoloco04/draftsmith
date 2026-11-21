package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rojoloco04/draftsmith/internal/riot"
)

// SettingsStore reads and changes Draftsmith's settings: the Riot API
// key, region, queues, the overlay, and the tracked accounts. serve
// implements it over .env and the archive's state, and keeps sync and
// the dashboard in step with each change.
type SettingsStore interface {
	Settings() Settings
	// SetKey checks key with Riot and saves it.
	SetKey(ctx context.Context, key string) error
	SetOptions(ctx context.Context, platform, queues string, overlay bool) error
	// AddAccount looks riotID up and tracks it, returning the Riot ID as
	// Riot spells it.
	AddAccount(ctx context.Context, riotID string) (string, error)
	// RemoveAccount stops tracking an account. Its archived games stay on
	// disk, so adding it back doesn't refetch them.
	RemoveAccount(ctx context.Context, puuid string) error
}

// Settings is what the settings page shows.
type Settings struct {
	KeySet bool
	// KeyHint is the key's last few characters, to tell keys apart
	// without showing it.
	KeyHint  string
	Platform string
	Queues   string // solo, flex or both
	Overlay  bool
	Accounts []SettingsAccount
	// File is where the settings are saved.
	File string
}

// SettingsAccount is a tracked account on the settings page.
type SettingsAccount struct {
	PUUID, RiotID, Platform string
	LastSync                time.Time
}

// SetSettings turns on the settings page.
func (s *Server) SetSettings(st SettingsStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = st
}

func (s *Server) settingsStore() SettingsStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// archive guards the pages that show the archive: with no account yet
// they send you to settings, and during the first sync they show its
// progress instead of an empty dashboard.
func (s *Server) archive(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case len(s.accts()) == 0 && s.settingsStore() != nil:
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/settings")
				return
			}
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
		case len(s.accts()) == 0:
			s.fail(w, r, http.StatusServiceUnavailable, errors.New("no accounts are tracked yet; run `draftsmith sync --riot-id Name#TAG` first"))
		case s.getStatus().FirstSync:
			s.render(w, r, "firstsync", "", s.base(r, "First sync", ""))
		default:
			h(w, r)
		}
	}
}

// firstSyncStatus is the first-sync page's poll. Once the sync is done
// the page reloads into the dashboard.
func (s *Server) firstSyncStatus(w http.ResponseWriter, r *http.Request) {
	st := s.getStatus()
	if !st.FirstSync {
		w.Header().Set("HX-Refresh", "true")
	}
	s.render(w, r, "firstsync", "progress", st)
}

type settingsData struct {
	Base
	Settings  Settings
	Platforms []riot.Platform
	// Msg and Err report the last change; Section says which form made it.
	Msg, Err, Section string
	// Local is false when the page is viewed from another device (over
	// Tailscale, say): it can look but not change anything.
	Local bool
}

func (s *Server) settingsData(r *http.Request) (settingsData, bool) {
	st := s.settingsStore()
	if st == nil {
		return settingsData{}, false
	}
	return settingsData{
		Base: s.base(r, "Settings", "settings"), Settings: st.Settings(), Platforms: riot.Platforms,
		Local: isLoopback(r),
	}, true
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	d, ok := s.settingsData(r)
	if !ok {
		s.fail(w, r, http.StatusNotFound, errors.New("settings are only available when Draftsmith runs as an app or with serve"))
		return
	}
	s.render(w, r, "settings", "", d)
}

// settingsChange runs one settings form: only from htmx (a cross-site
// form can't set HX-Request) and only from this PC, since the dashboard
// has no login. It answers with the settings body, with the change's
// result next to its form.
func (s *Server) settingsChange(w http.ResponseWriter, r *http.Request, section string, change func(SettingsStore) (string, error)) {
	if r.Header.Get("HX-Request") != "true" {
		http.Error(w, "change settings from the settings page", http.StatusForbidden)
		return
	}
	if !isLoopback(r) {
		http.Error(w, "Settings can only be changed on the PC Draftsmith runs on.", http.StatusForbidden)
		return
	}
	st := s.settingsStore()
	if st == nil {
		http.Error(w, "settings are off", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	msg, err := change(st)
	d, _ := s.settingsData(r)
	d.Section, d.Msg = section, msg
	if err != nil {
		d.Msg, d.Err = "", sentence(err.Error())
	}
	s.render(w, r, "settings", "settingsBody", d)
}

func (s *Server) settingsKey(w http.ResponseWriter, r *http.Request) {
	s.settingsChange(w, r, "key", func(st SettingsStore) (string, error) {
		key := strings.Trim(strings.TrimSpace(r.PostFormValue("key")), `"'`)
		if key == "" {
			return "", errors.New("paste your key first")
		}
		if err := st.SetKey(r.Context(), key); err != nil {
			return "", err
		}
		s.SetStatus(func(s *Status) { s.KeyRejected = false })
		if len(s.accts()) == 0 {
			return "Riot accepted the key. Now add your Riot ID below.", nil
		}
		return "Riot accepted the key. Syncing now.", nil
	})
}

func (s *Server) settingsOptions(w http.ResponseWriter, r *http.Request) {
	s.settingsChange(w, r, "options", func(st SettingsStore) (string, error) {
		err := st.SetOptions(r.Context(), r.PostFormValue("platform"), r.PostFormValue("queues"), r.PostFormValue("overlay") == "on")
		return "Saved.", err
	})
}

func (s *Server) settingsAddAccount(w http.ResponseWriter, r *http.Request) {
	first := len(s.accts()) == 0
	s.settingsChange(w, r, "accounts", func(st SettingsStore) (string, error) {
		id, err := st.AddAccount(r.Context(), r.PostFormValue("riot_id"))
		if err != nil {
			return "", err
		}
		if first {
			// The first account: go watch its games come in.
			w.Header().Set("HX-Redirect", "/")
		}
		return "Added " + id + ". Its games are syncing now.", nil
	})
}

func (s *Server) settingsRemoveAccount(w http.ResponseWriter, r *http.Request) {
	s.settingsChange(w, r, "accounts", func(st SettingsStore) (string, error) {
		err := st.RemoveAccount(r.Context(), r.PostFormValue("puuid"))
		return "Removed. Its games stay in the archive if you add it back.", err
	})
}

// isLoopback reports whether r came from this machine.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
