package local

import (
	"reflect"
	"testing"
)

func TestParseDeviceIDsAll(t *testing.T) {
	ids, selectGPU, err := ParseDeviceIDs("all")
	if err != nil {
		t.Fatalf("ParseDeviceIDs() error = %v", err)
	}
	if ids != nil || selectGPU {
		t.Fatalf("ids = %#v select = %v", ids, selectGPU)
	}
}

func TestParseDeviceIDsSelect(t *testing.T) {
	ids, selectGPU, err := ParseDeviceIDs("select")
	if err != nil {
		t.Fatalf("ParseDeviceIDs() error = %v", err)
	}
	if ids != nil || !selectGPU {
		t.Fatalf("ids = %#v select = %v", ids, selectGPU)
	}
}

func TestParseDeviceIDsList(t *testing.T) {
	ids, selectGPU, err := ParseDeviceIDs("0,2,2")
	if err != nil {
		t.Fatalf("ParseDeviceIDs() error = %v", err)
	}
	if selectGPU {
		t.Fatal("selectGPU = true")
	}
	if want := []int{0, 2}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %#v, want %#v", ids, want)
	}
}

func TestParseDeviceIDsRejectsNegative(t *testing.T) {
	if _, _, err := ParseDeviceIDs("-1"); err == nil {
		t.Fatal("expected negative id error")
	}
}
