// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Acmefmt watches acme for source files being written.
// Each time a source file is written, acmefmt checks whether the
// import block needs adjustment. If so, it makes the changes
// in the window body but does not write the file.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"9fans.net/go/acme"
)

var (
	dofmt   = flag.Bool("f", true, "run formatter on the entire file after Put")
	fmtcmd  = flag.String("c", "", "custom formatter")
	fmtexts = flag.String("e", "", "custom extensions")

	cexts  = []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"}
	goexts = []string{".go"}
)

func main() {
	flag.Parse()

	l, err := acme.Log()
	if err != nil {
		log.Fatal(err)
	}

	for {
		event, err := l.Read()
		if err != nil {
			log.Fatal(err)
		}
		if event.Name != "" && event.Op == "put" {
			switch exts := strings.Split(*fmtexts, " "); {
			case *fmtexts != "" && matchext(event.Name, exts):
				reformat(event.ID, event.Name, *fmtcmd)
			case matchext(event.Name, cexts):
				reformat(event.ID, event.Name, "clang-format")
			case matchext(event.Name, goexts):
				reformat(event.ID, event.Name, "goimports")
			}
		}
	}
}

func matchext(name string, exts []string) bool {
	for _, ext := range exts {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func reformat(id int, name, cmd string) {
	w, err := acme.Open(id, nil)
	if err != nil {
		log.Print(err)
		return
	}
	defer w.CloseFiles()

	old, err := ioutil.ReadFile(name)
	if err != nil {
		//log.Print(err)
		return
	}
	new, err := exec.Command(cmd, name).CombinedOutput()
	if err != nil {
		if strings.Contains(string(new), "fatal error") {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n%s", cmd, name, err, new)
		} else {
			fmt.Fprintf(os.Stderr, "%s", new)
		}
		return
	}

	if bytes.Equal(old, new) {
		return
	}

	if !*dofmt {
		oldTop, err := readImports(bytes.NewReader(old), true)
		if err != nil {
			//log.Print(err)
			return
		}
		newTop, err := readImports(bytes.NewReader(new), true)
		if err != nil {
			//log.Print(err)
			return
		}
		if bytes.Equal(oldTop, newTop) {
			return
		}
		w.Addr("0,#%d", utf8.RuneCount(oldTop))
		w.Write("data", newTop)
		return
	}

	f, err := ioutil.TempFile("", "acmefmt")
	if err != nil {
		log.Print(err)
		return
	}
	if _, err := f.Write(new); err != nil {
		log.Print(err)
		return
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)

	diff, _ := exec.Command("9", "diff", name, tmp).CombinedOutput()

	latest, err := w.ReadAll("body")
	if err != nil {
		log.Print(err)
		return
	}
	if !bytes.Equal(old, latest) {
		log.Printf("skipped update to %s: window modified since Put\n", name)
		return
	}

	w.Write("ctl", []byte("mark"))
	w.Write("ctl", []byte("nomark"))
	diffLines := strings.Split(string(diff), "\n")
	for i := len(diffLines) - 1; i >= 0; i-- {
		line := diffLines[i]
		if line == "" {
			continue
		}
		if line[0] == '<' || line[0] == '-' || line[0] == '>' {
			continue
		}
		j := 0
		for j < len(line) && line[j] != 'a' && line[j] != 'c' && line[j] != 'd' {
			j++
		}
		if j >= len(line) {
			log.Printf("cannot parse diff line: %q", line)
			break
		}
		oldStart, oldEnd := parseSpan(line[:j])
		newStart, newEnd := parseSpan(line[j+1:])
		if oldStart == 0 || newStart == 0 {
			continue
		}
		switch line[j] {
		case 'a':
			err := w.Addr("%d+#0", oldStart)
			if err != nil {
				log.Print(err)
				break
			}
			w.Write("data", findLines(new, newStart, newEnd))
		case 'c':
			err := w.Addr("%d,%d", oldStart, oldEnd)
			if err != nil {
				log.Print(err)
				break
			}
			w.Write("data", findLines(new, newStart, newEnd))
		case 'd':
			err := w.Addr("%d,%d", oldStart, oldEnd)
			if err != nil {
				log.Print(err)
				break
			}
			w.Write("data", nil)
		}
	}
}

func parseSpan(text string) (start, end int) {
	i := strings.Index(text, ",")
	if i < 0 {
		n, err := strconv.Atoi(text)
		if err != nil {
			log.Printf("cannot parse span %q", text)
			return 0, 0
		}
		return n, n
	}
	start, err1 := strconv.Atoi(text[:i])
	end, err2 := strconv.Atoi(text[i+1:])
	if err1 != nil || err2 != nil {
		log.Printf("cannot parse span %q", text)
		return 0, 0
	}
	return start, end
}

func findLines(text []byte, start, end int) []byte {
	i := 0

	start--
	for ; i < len(text) && start > 0; i++ {
		if text[i] == '\n' {
			start--
			end--
		}
	}
	startByte := i
	for ; i < len(text) && end > 0; i++ {
		if text[i] == '\n' {
			end--
		}
	}
	endByte := i
	return text[startByte:endByte]
}
