package plan

import "testing"

func TestInternalLink(t *testing.T) {
	d, err := SelectDriver("go1.26.5", false)
	if err != nil {
		t.Fatal(err)
	}
	const compiler = "/toolchain/bin/gcc"
	const start = "GOROOT='' /go/pkg/tool/linux_amd64/link "
	cases := []struct {
		name, flags string
		want        bool
	}{
		{"explicit", "-linkmode=internal -extld=" + compiler, true},
		{"separate operands", "--linkmode internal --extld " + compiler, true},
		{"last internal", "-linkmode=external -linkmode=internal -extld=" + compiler, true},
		{"last external", "-linkmode=internal -linkmode=external -extld=" + compiler, false},
		{"auto", "-linkmode=auto -extld=" + compiler, false},
		{"implicit", "-extld=" + compiler, false},
		{"quoted operand", "-X 'main.value=-linkmode=internal' -extld=" + compiler, false},
		{"flag operand", "-buildid -linkmode=internal -extld=" + compiler, false},
		{"option terminator", "-- -linkmode=internal -extld=" + compiler, false},
		{"positional terminator", "input.a -linkmode=internal -extld=" + compiler, false},
		{"custom compiler", "-linkmode=internal -extld=/custom/gcc", false},
		{"compiler options", "-linkmode=internal -extld='" + compiler + " -static'", false},
		{"last compiler", "-linkmode=internal -extld=/custom/gcc -extld=" + compiler, true},
		{"last custom compiler", "-linkmode=internal -extld=" + compiler + " -extld=/custom/gcc", false},
		{"missing compiler", "-linkmode=internal", false},
		{"explicit libgcc", "-libgcc=/custom/libgcc.a -linkmode=internal -extld=" + compiler, true},
		{"disabled libgcc", "-libgcc=none -linkmode=internal -extld=" + compiler, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := d.InternalLink(start+tc.flags+" input.a", compiler); got != tc.want {
				t.Fatalf("InternalLink(%q) = %v, want %v", tc.flags, got, tc.want)
			}
		})
	}
	valid := start + "-linkmode=internal -extld=" + compiler + " input.a"
	for _, raw := range []string{
		valid + "\n" + valid,
		"cat >importcfg << 'EOF' # internal\n" + valid + "\nEOF",
		valid + "\ncat >importcfg << 'EOF' # internal\nunterminated",
		start + "-linkmode='internal -extld=" + compiler,
		start + "-linkmode=internal -extld=" + compiler + " -libgcc",
	} {
		if d.InternalLink(raw, compiler) {
			t.Fatalf("accepted ambiguous trace %q", raw)
		}
	}
	if !d.InternalLink("cat >importcfg << 'EOF' # internal\n"+valid+"\nEOF\n"+valid, compiler) {
		t.Fatal("file data confused root link selection")
	}
	d.Experimental = true
	if d.InternalLink(valid, compiler) {
		t.Fatal("optimized unverified SDK")
	}
}
