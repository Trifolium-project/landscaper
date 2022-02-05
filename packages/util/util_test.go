package util

import "testing"



func TestContains(t *testing.T) {
	value := "test"

	list := []string{"test1", "test2", "test"}

	if !(Contains(list, value) && true) {
		t.Error("Expected true, got ", false)
	}
}