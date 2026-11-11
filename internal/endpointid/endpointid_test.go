package endpointid

import "testing"

func TestKey(t *testing.T) {
	const want = "23eaffe2-3f9c-4c86-9593-1ee8f322891e"
	tests := []struct {
		in     string
		key    string
		wantOK bool
	}{
		{"me/ep/I-r_4j-cTIaVkx7o8yKJHg", want, true},
		{"ep/I-r_4j-cTIaVkx7o8yKJHg", want, true},
		{"0/ep/I-r_4j-cTIaVkx7o8yKJHg", want, true},
		{"port1/principals/p1/endpoints/23eaffe2-3f9c-4c86-9593-1ee8f322891e", want, true},
		{"port1/principals/p1/endpoints/23EAFFE2-3F9C-4C86-9593-1EE8F322891E", want, true},
		{"me/ep/I-r_4j-cTIaVkx7o8yKJHg=", "", false},
		{"me/ep/short", "", false},
		{"me/g/I-r_4j-cTIaVkx7o8yKJHg", "", false},
		{"port1/principals/p1/endpoints/not-a-uuid", "", false},
		{"my-vacuum", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		got, ok := Key(tc.in)
		if got != tc.key || ok != tc.wantOK {
			t.Errorf("Key(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.key, tc.wantOK)
		}
	}
}

func TestSame(t *testing.T) {
	if !Same("me/ep/I-r_4j-cTIaVkx7o8yKJHg", "port1/principals/p/endpoints/23eaffe2-3f9c-4c86-9593-1ee8f322891e") {
		t.Error("two spellings of one endpoint must be the same")
	}
	if Same("me/ep/I-r_4j-cTIaVkx7o8yKJHg", "me/ep/AAAAAAAAAAAAAAAAAAAAAA") {
		t.Error("different endpoints must differ")
	}
	if !Same("alias", "alias") || Same("alias", "other") {
		t.Error("unkeyed ids compare by spelling")
	}
}
