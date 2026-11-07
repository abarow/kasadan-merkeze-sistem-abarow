// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personnel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/outbox"
)

// memStore is an in-memory Store.
type memStore struct {
	users   map[string]User
	changes []Change
	saved   []SaveRequest
}

func newMemStore() *memStore { return &memStore{users: map[string]User{}} }

func (s *memStore) User(_ context.Context, id string) (User, error) {
	u, ok := s.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (s *memStore) Users(context.Context) ([]User, error) {
	var out []User
	for _, u := range s.users {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b User) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (s *memStore) Save(_ context.Context, r SaveRequest) error {
	if _, ok := s.users[r.User.ID]; ok && r.New {
		return ErrExists
	}
	s.users[r.User.ID] = r.User
	s.changes = append(s.changes, r.Change)
	s.saved = append(s.saved, r)
	return nil
}

func (s *memStore) History(_ context.Context, id string, limit int) ([]Change, error) {
	var out []Change
	for i := len(s.changes) - 1; i >= 0 && len(out) < limit; i-- {
		if s.changes[i].ID == id {
			out = append(out, s.changes[i])
		}
	}
	return out, nil
}

// fast sets a plain digest so that tests do not wait for bcrypt.
func fast(m *Manage) *Manage {
	m.HashPIN = func(p string) ([]byte, error) { return []byte("pin:" + p), nil }
	m.HashPassword = func(p string) (string, error) { return "parola:" + p, nil }
	return m
}

type countEncoder struct{ n int }

func (e *countEncoder) Encode(u User) ([]outbox.Message, error) {
	e.n++
	return []outbox.Message{{ID: "m-" + u.ID, Subject: "config.cashier." + u.ID}}, nil
}

var (
	t0      = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	manager = Actor{ID: "mudur", Name: "Müdür Hanım", Manage: true}
)

func storeManage(st Store, enc Encoder) *Manage {
	m := fast(NewManage(st, Config{Roles: []Role{RoleCashier, RoleManager}, ByName: true, Encoder: enc}))
	m.SetClock(func() time.Time { return t0 })
	return m
}

func headquartersManage(st Store, closed *[]string) *Manage {
	cfg := Config{Roles: []Role{RoleAdmin, RoleCatalog, RoleReport}, Admins: []Role{RoleAdmin}}
	if closed != nil {
		cfg.CloseSessions = func(_ context.Context, id string) error { *closed = append(*closed, id); return nil }
	}
	m := fast(NewManage(st, cfg))
	m.SetClock(func() time.Time { return t0 })
	return m
}

func TestStoreStaffLifecycle(t *testing.T) {
	ctx := context.Background()
	st, enc := newMemStore(), &countEncoder{}
	m := storeManage(st, enc)

	u, err := m.Add(ctx, manager, "ayse", "1234", Input{Name: "  Ayşe  Yılmaz ", Role: RoleManager})
	if err != nil || u.Name != "Ayşe Yılmaz" || string(u.PINHash) != "pin:1234" || u.UpdatedBy != "Müdür Hanım" || !u.UpdatedAt.Equal(t0) {
		t.Fatalf("Add: %+v %v", u, err)
	}
	if len(st.saved[0].Msgs) != 1 || !st.saved[0].New || st.saved[0].CloseSessions {
		t.Errorf("record request: %+v", st.saved[0])
	}
	if _, err := m.Add(ctx, manager, "ayse", "1234", Input{Name: "Başka", Role: RoleCashier}); !errors.Is(err, ErrExists) {
		t.Errorf("same code: %v", err)
	}
	for name, tc := range map[string]struct {
		id, pin string
		in      Input
		want    error
	}{
		"kod":          {"Ayşe", "1234", Input{Name: "A", Role: RoleCashier}, ErrInvalidID},
		"ad":           {"x", "1234", Input{Name: "  ", Role: RoleCashier}, ErrInvalidName},
		"PIN":          {"x", "12", Input{Name: "A", Role: RoleCashier}, ErrInvalidPIN},
		"merkez rolü":  {"x", "1234", Input{Name: "A", Role: RoleAdmin}, ErrRoleNotManaged},
		"bilinmeyen":   {"x", "1234", Input{Name: "A", Role: "kral"}, ErrInvalidRole},
		"nokta içeren": {"a.b", "1234", Input{Name: "A", Role: RoleCashier}, ErrInvalidID},
	} {
		if _, err := m.Add(ctx, manager, tc.id, tc.pin, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := m.Add(ctx, Actor{ID: "x"}, "yeni", "1234", Input{Name: "A", Role: RoleCashier}); !errors.Is(err, ErrForbidden) {
		t.Errorf("yetkisiz: %v", err)
	}

	// Update: only what changed is written to the record; if there is no change there is no record.
	n := len(st.saved)
	if _, err := m.Update(ctx, manager, "ayse", Input{Name: "Ayşe Yılmaz", Role: RoleManager}); err != nil || len(st.saved) != n {
		t.Errorf("no change: %v, %d records", err, len(st.saved)-n)
	}
	if _, err := m.Update(ctx, manager, "ayse", Input{Name: "Ayşe Kaya", Role: RoleCashier}); err != nil {
		t.Fatal(err)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "ad: Ayşe Yılmaz → Ayşe Kaya; rol: müdür → kasiyer" {
		t.Errorf("record %q", got)
	}

	// When the PIN changes the sessions close; when the account closes too.
	if u, err := m.SetSecret(ctx, manager, "ayse", "5678"); err != nil || string(u.PINHash) != "pin:5678" || !st.saved[len(st.saved)-1].CloseSessions {
		t.Errorf("SetSecret: %+v %v", u, err)
	}
	if _, err := m.SetSecret(ctx, manager, "ayse", "abc"); !errors.Is(err, ErrInvalidPIN) {
		t.Errorf("bad PIN: %v", err)
	}
	if _, err := m.SetDisabled(ctx, manager, "ayse", true); err != nil || !st.users["ayse"].Disabled || !st.saved[len(st.saved)-1].CloseSessions {
		t.Errorf("kapatma: %v", err)
	}
	n = len(st.saved)
	if _, err := m.SetDisabled(ctx, manager, "ayse", true); err != nil || len(st.saved) != n {
		t.Errorf("already closed: %v", err)
	}
	if _, err := m.SetDisabled(ctx, manager, "ayse", false); err != nil || st.saved[len(st.saved)-1].CloseSessions {
		t.Errorf("opening does not close sessions: %v", err)
	}
	if _, _, err := m.Get(ctx, manager, "yok"); !errors.Is(err, ErrNotFound) {
		t.Errorf("olmayan: %v", err)
	}
	if _, h, err := m.Get(ctx, manager, "ayse"); err != nil || len(h) != 5 || h[0].Detail != "hesap açıldı" {
		t.Errorf("history: %+v %v", h, err)
	}
	// Every record is propagated (a second add with the same code was rejected before reaching a record).
	if enc.n != len(st.saved)+1 {
		t.Errorf("propagation %d times, record %d times", enc.n, len(st.saved))
	}
}

func TestListOrdersByNameOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	m := storeManage(st, nil)
	for _, c := range []struct{ id, name string }{{"a", "Zeynep"}, {"b", "Çiğdem"}, {"c", "Ali"}, {"d", "Işık"}, {"e", "İnci"}} {
		if _, err := m.Add(ctx, manager, c.id, "1234", Input{Name: c.name, Role: RoleCashier}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.SetDisabled(ctx, manager, "c", true); err != nil {
		t.Fatal(err)
	}
	var got []string
	list, _ := m.List(ctx, manager)
	for _, u := range list {
		got = append(got, u.Name)
	}
	// Open accounts first, in Turkish alphabet order (C, Ç; I, İ); closed last.
	if want := []string{"Çiğdem", "Işık", "İnci", "Zeynep", "Ali"}; !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
	names, _ := m.Names(ctx)
	if names["a"] != "Zeynep" || len(names) != 5 {
		t.Errorf("adlar %v", names)
	}

	byCode := fast(NewManage(st, Config{Roles: []Role{RoleCashier}}))
	list, _ = byCode.List(ctx, manager)
	if list[0].ID != "a" || list[4].ID != "e" {
		t.Errorf("code order broke: %v", list)
	}
}

func TestHeadquartersUsers(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	var closed []string
	m := headquartersManage(st, &closed)
	root := Actor{ID: "ayse", Name: "Ayşe", Manage: true}

	// The username is lower-cased; if the name is empty, the username.
	u, err := m.Add(ctx, root, "AYSE", "uzun-parola-12", Input{Role: RoleAdmin})
	if err != nil || u.ID != "ayse" || u.Name != "ayse" || !u.AllStores || u.PasswordHash != "parola:uzun-parola-12" || u.PINHash != nil {
		t.Fatalf("administrator: %+v %v", u, err)
	}
	if got := st.changes[0].Detail; got != "eklendi: ayse (yönetici; bütün mağazalar)" {
		t.Errorf("record %q", got)
	}
	// Scope: a store is needed for a non-administrator; the list is sorted and without repeats.
	if _, err := m.Add(ctx, root, "ali", "uzun-parola-12", Input{Name: "Ali", Role: RoleReport}); !errors.Is(err, ErrNoStores) {
		t.Errorf("without stores: %v", err)
	}
	if _, err := m.Add(ctx, root, "ali", "uzun-parola-12", Input{Name: "Ali", Role: RoleReport, Stores: []string{"a b"}}); !errors.Is(err, ErrInvalidStore) {
		t.Errorf("bad store: %v", err)
	}
	ali, err := m.Add(ctx, root, "ali", "uzun-parola-12", Input{Name: "Ali", Role: RoleReport, Stores: []string{"m2", "m1", "m2"}})
	if err != nil || !slices.Equal(ali.Stores, []string{"m1", "m2"}) || ali.AllStores {
		t.Fatalf("report user: %+v %v", ali, err)
	}
	if !ali.SeesStore("m1") || ali.SeesStore("m3") || !u.SeesStore("m3") || ali.CanManageUsers() || !u.CanEditCatalog() {
		t.Error("scope rules")
	}
	if _, err := m.Add(ctx, root, "kisa", "kisa", Input{Name: "K", Role: RoleReport, AllStores: true}); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short password: %v", err)
	}
	if _, err := m.Add(ctx, root, "x", "uzun-parola-12", Input{Name: "X", Role: RoleCashier}); !errors.Is(err, ErrRoleNotManaged) {
		t.Errorf("store staff at headquarters: %v", err)
	}
	if _, err := m.Add(ctx, root, "Çağrı", "uzun-parola-12", Input{Name: "X", Role: RoleReport, AllStores: true}); !errors.Is(err, ErrInvalidUsername) {
		t.Errorf("Turkish username: %v", err)
	}

	// A store change is written to the record; the username is found in upper case too.
	if _, err := m.Update(ctx, root, "ALI", Input{Name: "Ali", Role: RoleCatalog, Stores: []string{"m2"}}); err != nil {
		t.Fatal(err)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "rol: rapor → katalog; mağazalar: m1, m2 → m2" {
		t.Errorf("record %q", got)
	}
	// A role cannot change the credential kind.
	if _, err := m.Update(ctx, root, "ali", Input{Name: "Ali", Role: RoleManager}); !errors.Is(err, ErrRoleNotManaged) {
		t.Errorf("headquarters → store role: %v", err)
	}

	// When the password changes the sessions close; also when someone changes their own.
	if _, err := m.SetSecret(ctx, root, "ali", "yeni-parola-12"); err != nil || !slices.Equal(closed, []string{"ali"}) {
		t.Errorf("parola: %v %v", err, closed)
	}
	if _, err := m.ChangeOwnSecret(ctx, Actor{ID: "ali", Name: "Ali"}, "bir-baska-12"); err != nil || st.users["ali"].PasswordHash != "parola:bir-baska-12" {
		t.Errorf("own password: %v", err)
	}
	if got := st.changes[len(st.changes)-1]; got.Detail != "parola değişti (kendisi)" || got.By != "Ali" {
		t.Errorf("record %+v", got)
	}

	// An administrator cannot lock themselves out; the last administrator is protected.
	if _, err := m.SetDisabled(ctx, root, "ayse", true); !errors.Is(err, ErrSelf) {
		t.Errorf("kendini kapatma: %v", err)
	}
	if _, err := m.Update(ctx, root, "ayse", Input{Name: "Ayşe", Role: RoleReport, AllStores: true}); !errors.Is(err, ErrSelf) {
		t.Errorf("lowering own role: %v", err)
	}
	other := Actor{ID: "baska", Name: "Başka", Manage: true}
	if _, err := m.SetDisabled(ctx, other, "ayse", true); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("last administrator: %v", err)
	}
	if _, err := m.Add(ctx, root, "veli", "uzun-parola-12", Input{Name: "Veli", Role: RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDisabled(ctx, other, "ayse", true); err != nil {
		t.Errorf("while there is another administrator: %v", err)
	}
	// The role of a closed administrator can change (they cannot sign in anyway).
	if _, err := m.Update(ctx, other, "ayse", Input{Name: "Ayşe", Role: RoleReport, AllStores: true}); err != nil {
		t.Errorf("closed administrator: %v", err)
	}
	if _, _, err := m.Get(ctx, Actor{ID: "x"}, "ayse"); !errors.Is(err, ErrForbidden) {
		t.Errorf("yetkisiz okuma: %v", err)
	}
}

func TestImport(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	m := storeManage(st, nil)
	if _, err := m.Add(ctx, manager, "ayse", "1111", Input{Name: "Ayşe", Role: RoleCashier}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDisabled(ctx, manager, "ayse", true); err != nil {
		t.Fatal(err)
	}
	n, err := m.Import(ctx, manager, []Seed{{"ayse", "Ayşe Yılmaz", RoleManager, "2222"}, {"ali", "Ali", RoleCashier, "3333"}})
	if err != nil || n != 2 {
		t.Fatalf("Import: %d %v", n, err)
	}
	if u := st.users["ayse"]; u.Name != "Ayşe Yılmaz" || u.Role != RoleManager || string(u.PINHash) != "pin:2222" || !u.Disabled {
		t.Errorf("var olan: %+v", u)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "eklendi; dosyadan yüklendi: Ali (kasiyer)" {
		t.Errorf("record of the new person %q", got)
	}

	// If there is an error none is loaded.
	before := len(st.saved)
	for name, seeds := range map[string][]Seed{
		"kod":      {{"ok", "Ok", RoleCashier, "1234"}, {"Kötü", "K", RoleCashier, "1234"}},
		"tekrar":   {{"a", "A", RoleCashier, "1234"}, {"a", "B", RoleCashier, "1234"}},
		"PIN":      {{"a", "A", RoleCashier, "12"}},
		"rol":      {{"a", "A", RoleAdmin, "1234"}},
		"ad":       {{"a", " ", RoleCashier, "1234"}},
		"bilinmez": {{"a", "A", "kral", "1234"}},
	} {
		if _, err := m.Import(ctx, manager, seeds); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(st.saved) != before {
		t.Errorf("%d people were loaded from the faulty file", len(st.saved)-before)
	}
	if _, err := m.Import(ctx, Actor{}, nil); !errors.Is(err, ErrForbidden) {
		t.Errorf("yetkisiz: %v", err)
	}
}

func TestRoleTable(t *testing.T) {
	for r, want := range map[Role]struct {
		cred  Credential
		store bool
	}{
		RoleCashier: {CredentialPIN, true}, RoleManager: {CredentialPIN, true},
		RoleAdmin: {CredentialPassword, false}, RoleCatalog: {CredentialPassword, false}, RoleReport: {CredentialPassword, false},
		"kral": {0, false},
	} {
		if r.Credential() != want.cred || r.StoreStaff() != want.store || r.Known() != (want.cred != 0) {
			t.Errorf("%s: %v %v %v", r, r.Credential(), r.StoreStaff(), r.Known())
		}
	}
	// The rule at the register is as before: only the store staff roles.
	if ValidateRole(RoleAdmin) == nil || ValidateRole(RoleManager) != nil {
		t.Error("ValidateRole changed")
	}
	if RoleName(RoleAdmin) != "yönetici" || RoleName(RoleCatalog) != "katalog" || RoleName(RoleReport) != "rapor" || RoleName(RoleManager) != "müdür" || RoleName(RoleCashier) != "kasiyer" {
		t.Error("role names")
	}
	if id, err := CleanID(RoleAdmin, " AYSE.Y "); err != nil || id != "ayse.y" {
		t.Errorf("CleanID: %q %v", id, err)
	}
	if _, err := CleanID(RoleCashier, "a.b"); err == nil {
		t.Error("kasiyer kodunda nokta kabul edildi")
	}
}

// If the sessions are bound to the credential's fingerprint (store server manager)
// the person's sessions are not deleted in bulk when the PIN changes; those whose fingerprint
// does not match close in Directory.Resolve.
func TestBoundSessionsDoNotCloseOnSecretChange(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	m := fast(NewManage(st, Config{Roles: []Role{RoleCashier, RoleManager}, BoundSessions: true}))
	if _, err := m.Add(ctx, manager, "ayse", "1234", Input{Name: "Ayşe", Role: RoleManager}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetSecret(ctx, manager, "ayse", "5678"); err != nil || st.saved[len(st.saved)-1].CloseSessions {
		t.Errorf("session closing was requested when the PIN changed: %v", err)
	}
	// Closing the account still closes the sessions.
	if _, err := m.SetDisabled(ctx, manager, "ayse", true); err != nil || !st.saved[len(st.saved)-1].CloseSessions {
		t.Errorf("session closing was not requested when the account closed: %v", err)
	}
}
