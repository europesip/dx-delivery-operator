package wcmexport

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func libNode(n int, title string) string {
	uuid := "u" + string(rune('0'+n))
	if title == "Portal Site" {
		uuid = PortalSiteUUID
	}
	return `<?xml version="1.0" encoding="UTF-8"?><icm:node xmlns:icm="http://www.ibm.com/software/data/cm/icm" icm:path="/contentRoot" icm:uuid="` + uuid + `">` +
		`<sv:node sv:name="icm:libraries[` + string(rune('0'+n)) + `]" xmlns:sv="http://www.jcp.org/jcr/sv/1.0">` +
		`<sv:property sv:name="icm:title" sv:type="String"><sv:value>` + title + `</sv:value></sv:property></sv:node></icm:node>`
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeExport builds root/554ee7f5/423dc4e4 with three libraries; the second has a folder with a space in its name.
func fakeExport(t *testing.T) string {
	root := t.TempDir()
	ws := filepath.Join(root, "554ee7f5", "423dc4e4")
	write(t, filepath.Join(ws, "aaaa1111.node"), libNode(4, "Portal Site"))
	write(t, filepath.Join(ws, "aaaa1111", "c1.node"), "<x/>")
	write(t, filepath.Join(ws, "bbbb2222.node"), libNode(5, "EuropeSip_ES &amp; more"))
	write(t, filepath.Join(ws, "bbbb2222", "home", "bus 2.node"), `<sv:node sv:name="bus 2"/>`)
	write(t, filepath.Join(ws, "bbbb2222", "home", "bus 2", "v1.value"), strings.Repeat("x", 5000))
	write(t, filepath.Join(ws, "cccc3333.node"), libNode(6, "EuropeSip_DS"))
	return root
}

func TestOpenFindsLibraries(t *testing.T) {
	exp, err := Open(fakeExport(t))
	if err != nil {
		t.Fatal(err)
	}
	if exp.WS != filepath.Join("554ee7f5", "423dc4e4") {
		t.Fatalf("ws = %s", exp.WS)
	}
	got := strings.Join(Titles(exp.Libraries), "|")
	if got != "EuropeSip_DS|EuropeSip_ES & more|Portal Site" {
		t.Fatalf("titles = %s", got)
	}
	for _, l := range exp.Libraries {
		if l.Title == "EuropeSip_ES & more" && (l.Files != 3 || l.Bytes < 5000) {
			t.Fatalf("size of %s: %d files %d bytes", l.Title, l.Files, l.Bytes)
		}
	}
}

func TestOpenRejectsOtherFolders(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "x.node"), "<not-a-library/>")
	if _, err := Open(root); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSelect(t *testing.T) {
	exp, _ := Open(fakeExport(t))
	all, left, err := Select(exp.Libraries, nil, nil, false)
	if err != nil || len(all) != 3 || len(left) != 0 {
		t.Fatalf("all: %v %v %v", all, left, err)
	}
	sel, left, err := Select(exp.Libraries, []string{"europesip_ds", "Portal Site"}, []string{"portal site"}, false)
	if err != nil || len(sel) != 1 || sel[0].Title != "EuropeSip_DS" {
		t.Fatalf("include+exclude: %v %v", sel, err)
	}
	if left["Portal Site"] != "in libraries.exclude" || left["EuropeSip_ES & more"] != "not in libraries.include" {
		t.Fatalf("left: %v", left)
	}
	sel, left, _ = Select(exp.Libraries, nil, nil, true)
	if len(sel) != 2 || !strings.Contains(left["Portal Site"], "excludeHCLDefaults") {
		t.Fatalf("hcl defaults: %v %v", sel, left)
	}
	if _, _, err := Select(exp.Libraries, []string{"Nope"}, nil, false); err == nil || !strings.Contains(err.Error(), "Nope") {
		t.Fatalf("missing include: %v", err)
	}
}

func TestChunk(t *testing.T) {
	libs := []Library{{Key: "a", Bytes: 300}, {Key: "b", Bytes: 250}, {Key: "c", Bytes: 200}, {Key: "d", Bytes: 900}, {Key: "e", Bytes: 50}}
	chunks := Chunk(libs, 500, 0)
	total := 0
	for _, c := range chunks {
		var s int64
		for _, l := range c {
			s += l.Bytes
			total++
		}
		if s > 500 && len(c) > 1 {
			t.Fatalf("chunk over the limit: %v", c)
		}
	}
	if total != 5 || len(chunks) != 3 {
		t.Fatalf("chunks: %v", chunks)
	}
}

func TestUnsafeName(t *testing.T) {
	for _, n := range []string{"9d0b88ba", "a_b-c.d", "Señal", "x(1)"} {
		if UnsafeName(n) {
			t.Fatalf("%q must be accepted", n)
		}
	}
	for _, n := range []string{"bus 2", "a\tb", "a:b", "a|b"} {
		if !UnsafeName(n) {
			t.Fatalf("%q must be rejected", n)
		}
	}
}

func TestUnsafePaths(t *testing.T) {
	exp, _ := Open(fakeExport(t))
	for _, l := range exp.Libraries {
		want := 0
		if l.Key == "bbbb2222" {
			want = 2 // "bus 2.node" and the folder "bus 2"
		}
		if len(l.UnsafePaths) != want {
			t.Fatalf("%s: %v", l.Title, l.UnsafePaths)
		}
	}
}

func TestWriteZip(t *testing.T) {
	exp, _ := Open(fakeExport(t))
	sel, _, _ := Select(exp.Libraries, []string{"EuropeSip_DS", "Portal Site"}, nil, false)
	out := filepath.Join(t.TempDir(), "c.zip")
	res, err := exp.WriteZip(sel, out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close() //nolint:errcheck
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := "554ee7f5/423dc4e4/aaaa1111.node,554ee7f5/423dc4e4/aaaa1111/c1.node,554ee7f5/423dc4e4/cccc3333.node"
	if strings.Join(names, ",") != want || res.Files != 3 {
		t.Fatalf("zip entries %v (%d files)", names, res.Files)
	}
}

func TestPortalSiteDetection(t *testing.T) {
	exp, _ := Open(fakeExport(t))
	n := 0
	for _, l := range exp.Libraries {
		if l.IsPortalSite() {
			n++
			if l.UUID != PortalSiteUUID {
				t.Fatalf("uuid %s", l.UUID)
			}
		}
	}
	if n != 1 {
		t.Fatalf("Portal Site found %d times", n)
	}
	if !(Library{Title: "Sitio del portal", UUID: PortalSiteUUID}).IsPortalSite() || (Library{Title: "Portal Sites"}).IsPortalSite() {
		t.Fatal("detection by ID, not by a similar title")
	}
}

func TestChunkByFiles(t *testing.T) {
	libs := []Library{{Key: "a", Bytes: 10, Files: 3000}, {Key: "b", Bytes: 10, Files: 2000}, {Key: "c", Bytes: 10, Files: 1500}, {Key: "d", Bytes: 10, Files: 9000}}
	chunks := Chunk(libs, 1<<30, 4000)
	for _, c := range chunks {
		n := 0
		for _, l := range c {
			n += l.Files
		}
		if n > 4000 && len(c) > 1 {
			t.Fatalf("chunk over the file limit: %v", c)
		}
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks %v", chunks)
	}
}

func TestOpenReadsDescription(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "554ee7f5", "423dc4e4")
	node := strings.Replace(libNode(1, "EuropeSip_DS"), `</sv:node></icm:node>`,
		`<sv:property sv:name="icm:description" sv:type="String"><sv:value>Biblioteca dise&#241;o &amp; m&#225;s</sv:value></sv:property></sv:node></icm:node>`, 1)
	write(t, filepath.Join(ws, "dddd4444.node"), node)
	write(t, filepath.Join(ws, "eeee5555.node"), libNode(2, "Sin descripcion"))
	exp, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range exp.Libraries {
		got[l.Title] = l.Description
	}
	if got["EuropeSip_DS"] != "Biblioteca diseño & más" || got["Sin descripcion"] != "" {
		t.Fatalf("descriptions: %#v", got)
	}
}
