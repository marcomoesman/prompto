package permission

import "testing"

func TestMode_Parse(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
	}{
		{"", ModeDefault},
		{"default", ModeDefault},
		{"acceptEdits", ModeAcceptEdits},
		{"accept-edits", ModeAcceptEdits},
		{"bypass", ModeBypass},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		if err != nil {
			t.Errorf("ParseMode(%q) err = %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestMode_ParseInvalid(t *testing.T) {
	if _, err := ParseMode("garbage"); err == nil {
		t.Error("expected error for garbage")
	}
}

func TestMode_String(t *testing.T) {
	if ModeDefault.String() != "default" {
		t.Errorf("ModeDefault.String() = %q", ModeDefault.String())
	}
	if ModeAcceptEdits.String() != "acceptEdits" {
		t.Errorf("ModeAcceptEdits.String() = %q", ModeAcceptEdits.String())
	}
	if ModeBypass.String() != "bypass" {
		t.Errorf("ModeBypass.String() = %q", ModeBypass.String())
	}
}

func TestMode_Cycle(t *testing.T) {
	if Cycle(ModeDefault) != ModeAcceptEdits {
		t.Errorf("Cycle(Default) = %v, want AcceptEdits", Cycle(ModeDefault))
	}
	if Cycle(ModeAcceptEdits) != ModeDefault {
		t.Errorf("Cycle(AcceptEdits) = %v, want Default", Cycle(ModeAcceptEdits))
	}
	if Cycle(ModeBypass) != ModeBypass {
		t.Error("Cycle(Bypass) should be sticky — no accidental exits")
	}
}
