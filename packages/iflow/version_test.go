package iflow

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "three components", value: "1.0.3", want: "1.0.3"},
		{name: "two components", value: "1.2", want: "1.2.0"},
		{name: "one component", value: "4", want: "4.0.0"},
		{name: "osgi qualifier", value: "1.0.3.qualifier", want: "1.0.3"},
		{name: "surrounding spaces", value: "  2.10.1  ", want: "2.10.1"},
		{name: "empty", value: "", wantErr: true},
		{name: "not a number", value: "1.x.3", wantErr: true},
		{name: "too many components", value: "1.2.3.4.5", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := ParseVersion(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseVersion(%q) expected an error, got %s", test.value, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseVersion(%q) returned error: %v", test.value, err)
			}
			if version.String() != test.want {
				t.Errorf("ParseVersion(%q) = %s, want %s", test.value, version.String(), test.want)
			}
		})
	}
}

func TestParseVersionKeepsRaw(t *testing.T) {
	version, err := ParseVersion("1.0.3.qualifier")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version.Raw != "1.0.3.qualifier" {
		t.Errorf("Raw = %q, want %q", version.Raw, "1.0.3.qualifier")
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "equal", a: "1.0.3", b: "1.0.3", want: 0},
		{name: "qualifier ignored", a: "1.0.3.a", b: "1.0.3.b", want: 0},
		{name: "patch lower", a: "1.0.2", b: "1.0.3", want: -1},
		{name: "patch higher", a: "1.0.4", b: "1.0.3", want: 1},
		{name: "minor wins over patch", a: "1.1.0", b: "1.0.9", want: 1},
		{name: "major wins over minor", a: "2.0.0", b: "1.9.9", want: 1},
		{name: "double digit patch", a: "1.0.10", b: "1.0.9", want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a, err := ParseVersion(test.a)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			b, err := ParseVersion(test.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := Compare(a, b); got != test.want {
				t.Errorf("Compare(%s, %s) = %d, want %d", test.a, test.b, got, test.want)
			}
		})
	}
}

func TestBump(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		level   string
		want    string
		wantErr bool
	}{
		{name: "patch", value: "1.0.3", level: BumpPatch, want: "1.0.4"},
		{name: "minor", value: "1.0.3", level: BumpMinor, want: "1.1.0"},
		{name: "major", value: "1.0.3", level: BumpMajor, want: "2.0.0"},
		{name: "case insensitive", value: "1.0.3", level: "PATCH", want: "1.0.4"},
		{name: "drops qualifier", value: "1.0.3.qualifier", level: BumpPatch, want: "1.0.4"},
		{name: "unknown level", value: "1.0.3", level: "build", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := ParseVersion(test.value)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			bumped, err := Bump(version, test.level)
			if test.wantErr {
				if err == nil {
					t.Fatalf("Bump(%s, %s) expected an error", test.value, test.level)
				}
				return
			}
			if err != nil {
				t.Fatalf("Bump returned error: %v", err)
			}
			if bumped.String() != test.want {
				t.Errorf("Bump(%s, %s) = %s, want %s", test.value, test.level, bumped.String(), test.want)
			}
			if bumped.Raw != test.want {
				t.Errorf("Bump(%s, %s).Raw = %s, want %s", test.value, test.level, bumped.Raw, test.want)
			}
		})
	}
}
