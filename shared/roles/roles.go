// Package roles — rollar, ruxsatlar va xodim yaratish huquqi.
//
// Yagona manba — shu papkadagi roles.json (hujjat 2.5); u binary ichiga embed qilinadi.
// NestJS backoffice ham xuddi shu faylni o'qiydi. Jadvalni o'zgartirish uchun faqat JSON tahrirlanadi.
// Ruxsat nomlari: "<bo'lim>:read" / "<bo'lim>:write" (bo'limlar — openapi AdminSection).
package roles

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type Role string

const (
	Passenger  Role = "passenger"
	Driver     Role = "driver"
	Superadmin Role = "superadmin"
	Owner      Role = "owner"
	Manager    Role = "manager"
	Marketer   Role = "marketer"
	Operator   Role = "operator"
)

//go:embed roles.json
var rolesJSON []byte

type entry struct {
	Kind        string   `json:"kind"`
	Permissions []string `json:"permissions"`
	Creatable   []Role   `json:"creatable_roles"`
}

type table struct {
	Sections []string        `json:"sections"`
	Roles    map[Role]*entry `json:"roles"`
}

// AdminSections — openapi AdminSection enum bilan bir xil (roles.json dan).
var AdminSections []string

var tbl = mustLoad(rolesJSON)

func init() { AdminSections = slices.Clone(tbl.Sections) }

func mustLoad(raw []byte) *table {
	t, err := parse(raw)
	if err != nil {
		panic("roles: roles.json: " + err.Error()) // embed qilingan fayl: xato dasturlash xatosi
	}
	return t
}

// parse — JSON'ni o'qiydi va izchilligini tekshiradi (noma'lum maydon, rol, bo'lim, ruxsat formati).
func parse(raw []byte) (*table, error) {
	var doc struct {
		table
		Comment string `json:"_comment"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	t := &doc.table

	known := []Role{Passenger, Driver, Superadmin, Owner, Manager, Marketer, Operator}
	if len(t.Roles) != len(known) {
		return nil, fmt.Errorf("rollar soni %d, kutilgan %d", len(t.Roles), len(known))
	}
	for _, r := range known {
		if t.Roles[r] == nil {
			return nil, fmt.Errorf("rol yo'q: %s", r)
		}
	}
	for r, e := range t.Roles {
		if e.Kind != "user" && e.Kind != "staff" {
			return nil, fmt.Errorf("%s: kind noto'g'ri: %q", r, e.Kind)
		}
		if len(e.Permissions) == 0 {
			return nil, fmt.Errorf("%s: ruxsatlar bo'sh", r)
		}
		if !sort.StringsAreSorted(e.Permissions) {
			return nil, fmt.Errorf("%s: ruxsatlar tartiblanmagan", r)
		}
		for i, p := range e.Permissions {
			if i > 0 && e.Permissions[i-1] == p {
				return nil, fmt.Errorf("%s: takroriy ruxsat %s", r, p)
			}
			section, action, ok := strings.Cut(p, ":")
			if !ok || section == "" || action == "" {
				return nil, fmt.Errorf("%s: ruxsat formati noto'g'ri: %q", r, p)
			}
			if e.Kind == "staff" && (!slices.Contains(t.Sections, section) || (action != "read" && action != "write")) {
				return nil, fmt.Errorf("%s: noma'lum bo'lim/amal: %q", r, p)
			}
		}
		if e.Kind == "user" && len(e.Creatable) > 0 {
			return nil, fmt.Errorf("%s: user rolida creatable_roles bo'lmaydi", r)
		}
		for _, c := range e.Creatable {
			if te := t.Roles[c]; te == nil || te.Kind != "staff" {
				return nil, fmt.Errorf("%s: yaratib bo'lmaydigan rol: %q", r, c)
			}
		}
	}
	return t, nil
}

// Permissions — rolning ruxsatlari (nusxa). Noma'lum rol uchun nil.
func (r Role) Permissions() []string {
	if e := tbl.Roles[r]; e != nil {
		return slices.Clone(e.Permissions)
	}
	return nil
}

// CreatableRoles — rol yarata oladigan xodim rollari (nusxa); huquqi yo'q rol uchun bo'sh.
func (r Role) CreatableRoles() []Role {
	if e := tbl.Roles[r]; e != nil {
		return slices.Clone(e.Creatable)
	}
	return nil
}

// CanCreate — r xodimi target rolidagi xodim yarata oladimi.
func (r Role) CanCreate(target Role) bool {
	e := tbl.Roles[r]
	return e != nil && slices.Contains(e.Creatable, target)
}

func (r Role) Valid() bool { return tbl.Roles[r] != nil }

// IsStaff — backoffice xodimi roli.
func (r Role) IsStaff() bool {
	e := tbl.Roles[r]
	return e != nil && e.Kind == "staff"
}
