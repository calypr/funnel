package logger

import (
	"reflect"
	"testing"
)

func TestArgListToMap(t *testing.T) {
	// single arg passed
	result := ArgListToMap(1)
	expected := map[string]interface{}{
		"unknown": 1,
	}
	if !reflect.DeepEqual(result, expected) {
		t.Log("expected:", expected)
		t.Log("actual:", result)
		t.Fatal("unexpected result")
	}

	// odd number of args
	result = ArgListToMap("foo", 1, 2)
	expected = map[string]interface{}{
		"unknown": 2, "foo": 1,
	}
	if !reflect.DeepEqual(result, expected) {
		t.Log("expected:", expected)
		t.Log("actual:", result)
		t.Fatal("unexpected result")
	}

	// normal case
	result = ArgListToMap("foo", "bar", "fizz", "buzz")
	expected = map[string]interface{}{
		"foo": "bar", "fizz": "buzz",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Log("expected:", expected)
		t.Log("actual:", result)
		t.Fatal("unexpected result")
	}

	// int as key
	result = ArgListToMap("foo", "bar", 1, "buzz")
	expected = map[string]interface{}{
		"foo": "bar", "1": "buzz",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Log("expected:", expected)
		t.Log("actual:", result)
		t.Fatal("unexpected result")
	}

	// duplicate keys
	// last value is kept
	result = ArgListToMap("foo", "bar", "foo", "buzz")
	expected = map[string]interface{}{
		"foo": "buzz",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Log("expected:", expected)
		t.Log("actual:", result)
		t.Fatal("unexpected result")
	}
}
