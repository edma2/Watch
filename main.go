// Copyright 2012 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Watch runs a command each time files in the current directory change.
//
// Usage:
//
//	Watch [-only pattern] cmd [args...]
//
// Watch opens a new acme window named for the current directory
// with a suffix of /+watch. The window shows the execution of the given
// command. Each time a file in that directory changes, Watch reexecutes
// the command and updates the window.
//
// TODO: dump state
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"9fans.net/go/acme"
)

var args []string
var win *acme.Win
var needrun = make(chan *acme.LogEvent, 1)
var pattern = flag.String("only", ".*", "only files that match regular expression")
var term = flag.Bool("t", false, "output stdout/stderr to terminal instead of an acme window")

func usage() {
	fmt.Fprintf(os.Stderr, "usage: Watch [-only pattern] cmd args...\n")
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flag.Parse()
	args = flag.Args()
	if len(args) == 0 {
		usage()
	}
	re := regexp.MustCompile(*pattern)
	pwd, _ := os.Getwd()
	needrun <- nil

	var err error
	if *term {
		go termRunner()
	} else {
		win, err = acme.New()
		if err != nil {
			log.Fatal(err)
		}
		win.Name(pwd + "/+watch")
		win.Ctl("clean")
		win.Fprintf("tag", "Get ")
		go events()
		go runner()
	}

	l, err := acme.Log()
	if err != nil {
		log.Fatal(err)
	}
	for {
		event, err := l.Read()
		if err != nil {
			log.Fatal(err)
		}
		if event.Name != "" && event.Op == "put" && strings.HasPrefix(event.Name, pwd) && re.MatchString(event.Name) {
			select {
			case needrun <- &event:
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func events() {
	for e := range win.EventChan() {
		switch e.C2 {
		case 'x', 'X': // execute
			if string(e.Text) == "Get" {
				select {
				case needrun <- nil:
				default:
				}
				continue
			}
			if string(e.Text) == "Del" {
				win.Ctl("delete")
			}
		}
		win.WriteEvent(e)
	}
	os.Exit(0)
}

var run struct {
	sync.Mutex
	id int
}

func envOf(event *acme.LogEvent) []string {
	var filtered []string
	for _, v := range os.Environ() {
		vv := strings.Split(v, "=")
		switch vv[0] {
		case "samfile", "%", "winid":
			continue
		default:
			filtered = append(filtered, v)
		}
	}
	if event == nil {
		return filtered
	}
	return append(
		filtered,
		"samfile="+event.Name,
		"%="+event.Name,
		fmt.Sprintf("winid=%d", event.ID))
}

func termRunner() {
	var lastcmd *exec.Cmd
	for event := range needrun {
		if lastcmd != nil {
			lastcmd.Process.Kill()
		}
		lastcmd = nil
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = envOf(event)
		if err := cmd.Start(); err != nil {
			continue
		}
		lastcmd = cmd
		go func() {
			_ = cmd.Wait()
		}()
	}
}

func runner() {
	var lastcmd *exec.Cmd
	for event := range needrun {
		run.Lock()
		run.id++
		id := run.id
		run.Unlock()
		if lastcmd != nil {
			lastcmd.Process.Kill()
		}
		lastcmd = nil
		cmd := exec.Command(args[0], args[1:]...)
		r, w, err := os.Pipe()
		if err != nil {
			log.Fatal(err)
		}
		win.Addr(",")
		win.Write("data", nil)
		win.Ctl("clean")
		win.Fprintf("body", "$ %s\n", strings.Join(args, " "))
		cmd.Stdout = w
		cmd.Stderr = w
		cmd.Env = envOf(event)
		if err := cmd.Start(); err != nil {
			r.Close()
			w.Close()
			win.Fprintf("body", "%s: %s\n", strings.Join(args, " "), err)
			continue
		}
		lastcmd = cmd
		w.Close()
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := r.Read(buf)
				if err != nil {
					break
				}
				run.Lock()
				if id == run.id {
					win.Write("body", buf[:n])
				}
				run.Unlock()
			}
			if err := cmd.Wait(); err != nil {
				run.Lock()
				if id == run.id {
					win.Fprintf("body", "%s: %s\n", strings.Join(args, " "), err)
				}
				run.Unlock()
			}
			win.Fprintf("body", "$\n")
			win.Fprintf("addr", "#0")
			win.Ctl("dot=addr")
			win.Ctl("show")
			win.Ctl("clean")
		}()
	}
}
