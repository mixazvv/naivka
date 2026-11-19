package main

import (
	"reflect"
	"strconv"
	"testing"
)

func BenchmarkSettingsSnapshot(b *testing.B) {
	s := DefaultSettings()
	for i := 0; i < 100; i++ {
		s.Servers = append(s.Servers, Server{ID: strconv.Itoa(i), Name: "Example", Type: "vless", Host: "server.example", Port: 443, Transport: "tcp", Security: "reality"})
		s.Rules = append(s.Rules, Rule{Kind: "domain", Value: strconv.Itoa(i) + ".example", Action: "proxy"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(s.clone().Servers) != 100 {
			b.Fatal("lost server")
		}
	}
}

// Populate every field, including future fields, so a shallow copy of any
// mutable value is caught even if the default configuration leaves it empty.
func TestSettingsSnapshotIsIndependent(t *testing.T) {
	var populate func(reflect.Value)
	populate = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				populate(v.Field(i))
			}
		case reflect.Slice:
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			populate(v.Index(0))
		case reflect.Map:
			v.Set(reflect.MakeMap(v.Type()))
			v.SetMapIndex(reflect.ValueOf("test"), reflect.ValueOf(1).Convert(v.Type().Elem()))
		case reflect.Ptr:
			v.Set(reflect.New(v.Type().Elem()))
			populate(v.Elem())
		case reflect.String:
			v.SetString("fixture")
		case reflect.Int, reflect.Int64:
			v.SetInt(1)
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Float64:
			v.SetFloat(0.5)
		default:
			t.Fatalf("add fixture for %v", v.Type())
		}
	}
	s := new(Settings)
	populate(reflect.ValueOf(s).Elem())
	c := s.clone()
	if !reflect.DeepEqual(s, c) {
		t.Fatal("snapshot changed values")
	}
	var check func(reflect.Value, reflect.Value)
	check = func(a, b reflect.Value) {
		switch a.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			if a.Pointer() == b.Pointer() {
				t.Fatalf("shared %v", a.Type())
			}
		}
		switch a.Kind() {
		case reflect.Struct:
			for i := 0; i < a.NumField(); i++ {
				check(a.Field(i), b.Field(i))
			}
		case reflect.Slice:
			check(a.Index(0), b.Index(0))
		case reflect.Ptr:
			check(a.Elem(), b.Elem())
		}
	}
	check(reflect.ValueOf(s), reflect.ValueOf(c))
	if s := DefaultSettings(); !reflect.DeepEqual(s, s.clone()) {
		t.Fatal("snapshot changed nil or empty fields")
	}
}
