// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"net/http"
	"reflect"
	"testing"
)

func TestPersonHeaders(t *testing.T) {
	for _, p := range []Person{
		{Username: "ayse", Name: "Ayşe Yılmaz, Kadıköy", Role: "report", Stores: []string{"store-1", "store-2"}},
		{Username: "headquarters", Name: "Merkez", Role: "admin", AllStores: true},
		{Username: "bos", Name: "Mağazasız", Role: "report"},
	} {
		h := http.Header{}
		h.Set(RoleHeader, "admin") // the header put by the client is overridden
		p.SetHeaders(h)
		got, ok := PersonFrom(h)
		if !ok || !reflect.DeepEqual(got, p) {
			t.Errorf("%+v → %+v %v", p, got, ok)
		}
	}
	h := http.Header{}
	h.Set(UserHeader, "sahte")
	StripHeaders(h)
	if _, ok := PersonFrom(h); ok {
		t.Error("a person was read with a deleted header")
	}
}
