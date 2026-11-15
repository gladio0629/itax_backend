package roles

import (
	"strings"
	"testing"
)

func TestRoles(t *testing.T) {
	all := []Role{Passenger, Driver, Superadmin, Owner, Manager, Marketer, Operator}
	for _, r := range all {
		if !r.Valid() || len(r.Permissions()) == 0 {
			t.Errorf("%s: ruxsatlar yo'q", r)
		}
	}
	for _, r := range []Role{Passenger, Driver} {
		if r.IsStaff() {
			t.Errorf("%s xodim emas", r)
		}
	}
	for _, r := range []Role{Superadmin, Owner, Manager, Marketer, Operator} {
		if !r.IsStaff() {
			t.Errorf("%s xodim", r)
		}
	}
	if Role("root").Valid() || Role("root").Permissions() != nil {
		t.Error("noma'lum rol yaroqli bo'lib qoldi")
	}
	// nusxa qaytadi: tashqaridan o'zgartirib bo'lmaydi
	p := Superadmin.Permissions()
	p[0] = "hacked"
	if Superadmin.Permissions()[0] == "hacked" {
		t.Error("Permissions() ichki massivni ochib qo'ydi")
	}
	// superadmin barcha bo'limlarga yozishi mumkin; marketer yoza olmaydi
	has := func(r Role, perm string) bool {
		for _, x := range r.Permissions() {
			if x == perm {
				return true
			}
		}
		return false
	}
	// audit — faqat o'qiladigan bo'lim (audit:write hech kimda yo'q); manager audit'ni ko'rmaydi
	for _, s := range AdminSections {
		if s == "audit" {
			continue
		}
		if !has(Superadmin, s+":write") {
			t.Errorf("superadmin %s:write yo'q", s)
		}
	}
	for _, r := range []Role{Owner, Manager} {
		for _, sec := range AdminSections {
			if sec == "audit" {
				continue
			}
			if !has(r, sec+":read") || !has(r, sec+":write") {
				t.Errorf("%s: %s read+write bo'lishi kerak", r, sec)
			}
		}
	}
	for _, r := range []Role{Superadmin, Owner, Manager, Marketer, Operator} {
		if has(r, "audit:write") {
			t.Errorf("%s: audit:write bo'lmasligi kerak", r)
		}
	}
	if !has(Superadmin, "audit:read") || !has(Owner, "audit:read") || has(Manager, "audit:read") || has(Operator, "audit:read") {
		t.Error("audit:read faqat superadmin va owner da")
	}
	if got := Marketer.Permissions(); len(got) != 1 || got[0] != "stats:read" {
		t.Errorf("marketer faqat stats:read: %v", got)
	}
	if got := Operator.Permissions(); len(got) != 4 || got[0] != "complaints:read" || got[1] != "complaints:write" ||
		got[2] != "operator:read" || got[3] != "operator:write" {
		t.Errorf("operator faqat complaints va operator read+write: %v", got)
	}
	// xodim yaratish huquqi alohida
	creates := map[Role][]Role{
		Superadmin: {Superadmin, Owner, Manager, Marketer, Operator},
		Owner:      {Manager, Marketer, Operator},
		Manager:    {Marketer, Operator},
		Marketer:   nil, Operator: nil, Passenger: nil, Driver: nil,
	}
	for r, want := range creates {
		got := r.CreatableRoles()
		if len(got) != len(want) {
			t.Errorf("%s yarata oladi: %v, kutilgan %v", r, got, want)
			continue
		}
		for _, w := range want {
			if !r.CanCreate(w) {
				t.Errorf("%s %s ni yarata olishi kerak", r, w)
			}
		}
	}
	if Owner.CanCreate(Owner) || Owner.CanCreate(Superadmin) || Manager.CanCreate(Manager) || Manager.CanCreate(Owner) {
		t.Error("owner/manager o'z darajasidagi yoki yuqori rolni yarata olmaydi")
	}
	if has(Passenger, "orders:accept") || !has(Driver, "orders:accept") {
		t.Error("passenger/driver ruxsatlari chalkashgan")
	}
}

func TestParseRejectsBrokenTable(t *testing.T) {
	good := string(rolesJSON)
	if _, err := parse([]byte(good)); err != nil {
		t.Fatalf("roles.json yaroqsiz: %v", err)
	}
	mutations := map[string]func(string) string{
		"noma'lum maydon":     func(s string) string { return strings.Replace(s, `"kind"`, `"kinds"`, 1) },
		"noma'lum bo'lim":     func(s string) string { return strings.Replace(s, `"stats:read"`, `"nope:read"`, 1) },
		"noma'lum amal":       func(s string) string { return strings.Replace(s, `"stats:read"`, `"stats:delete"`, 1) },
		"yaratib bo'lmas rol": func(s string) string { return strings.Replace(s, `"manager",`, `"driver",`, 1) },
		"rol yetishmaydi":     func(s string) string { return strings.Replace(s, `"marketer": {`, `"marketer2": {`, 1) },
		"buzuq JSON":          func(s string) string { return s[:len(s)/2] },
	}
	for name, m := range mutations {
		if _, err := parse([]byte(m(good))); err == nil {
			t.Errorf("%s: xato kutilgan edi", name)
		}
	}
}
