package main

import "testing"

func TestValidateFlags(t *testing.T) {
	cases := []struct {
		name    string
		regen   bool
		levels  string
		report  bool
		wantErr bool
	}{
		{"regen alone is fine", true, "", false, false},
		{"regen with levels is refused", true, "L0", false, true},
		{"regen with report is refused", true, "", true, true},
		{"levels required without regen", false, "", false, true},
		{"levels alone is fine", false, "L0", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateFlags(c.regen, c.levels, c.report)
			if (err != nil) != c.wantErr {
				t.Errorf("validateFlags(%v, %q, %v) = %v, want err = %v", c.regen, c.levels, c.report, err, c.wantErr)
			}
		})
	}
}
