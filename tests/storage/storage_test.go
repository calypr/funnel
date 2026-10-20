package storage

import (
	"os"
	"path"
	"strings"
	"syscall"
	"testing"

	"github.com/calypr/funnel/logger"
	"github.com/calypr/funnel/tes"
	"github.com/calypr/funnel/tests"
)

var log = logger.NewLogger("funnel-e2e-storage", tests.LogConfig())
var fun *tests.Funnel
var conf = tests.DefaultConfig()

func TestMain(m *testing.M) {
	tests.ParseConfig()
	conf = tests.DefaultConfig()
	conf.Worker.LeaveWorkDir = true
	fun = tests.NewFunnel(conf)
	fun.StartServer()
	os.Exit(m.Run())
}

// Test that a file can be passed as an input and output.
func TestFileMount(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'cat $in > $out'
    -i in=./testdata/test_in
    -o out={{ .storage }}/test_out
  `)
	task := fun.Wait(id)
	c := fun.ReadFile("test_out")
	if c != "hello\n" {
		t.Fatal("Unexpected output file", task, c)
	}
}

// Test that the local storage system hard links input files.
// TODO this test is unix specific because it uses syscall?
func TestLocalFilesystemHardLinkInput(t *testing.T) {
	tests.SetLogOutput(log, t)
	fun.WriteFile("test_hard_link_input", "content")
	id := fun.Run(`
    --sh "echo foo"
    -i in={{ .storage }}/test_hard_link_input
  `)
	task := fun.Wait(id)
	if task.State != tes.State_COMPLETE {
		t.Fatal("unexpected task failure")
	}
	name := fun.StorageDir + "/test_hard_link_input"
	fi, sterr := os.Lstat(name)
	if sterr != nil {
		panic(sterr)
	}
	s, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		panic("can't retrieve Stat_t")
	}
	if uint16(s.Nlink) != uint16(2) {
		t.Fatal("expected two links", s.Nlink)
	}
}

// Test using a symlink as an input file.
func TestSymlinkInput(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'cat $in > $out'
    -i in=./testdata/test_in_symlink
    -o out={{ .storage }}/test_out
  `)
	task := fun.Wait(id)
	if task.State != tes.State_COMPLETE {
		t.Fatal("Expected success on symlink input")
	}
}

// Test using a broken symlink as an input file.
func TestBrokenSymlinkInput(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'cat $in > $out'
    -i in=./testdata/test_broken_symlink
    -o out={{ .storage }}/test_out
  `)
	task := fun.Wait(id)
	if task.State != tes.State_SYSTEM_ERROR {
		t.Fatal("Expected error on broken symlink input")
	}
}

/*
Test the case where a container creates a symlink in an output path.
From the view of the host system where Funnel is running, this creates
a broken link, because the source of the symlink is a path relative
to the container filesystem.

Funnel can fix some of these cases using volume definitions, which
is being tested here.
*/
func TestSymlinkOutput(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo foo > $dir/foo && ln -s $dir/foo $dir/sym && ln -s $dir/foo $sym'
    -o sym={{ .storage }}/out-sym
    -O dir={{ .storage }}/out-dir
  `)
	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected success on symlink output")
	}

	if fun.ReadFile("out-dir/foo") != "foo\n" {
		t.Fatal("unexpected out-dir/foo content")
	}

	if fun.ReadFile("out-sym") != "foo\n" {
		t.Fatal("unexpected out-sym content")
	}

	if fun.ReadFile("out-dir/sym") != "foo\n" {
		t.Fatal("unexpected out-dir/sym content")
	}
}

func TestOverwriteOutput(t *testing.T) {
	tests.SetLogOutput(log, t)
	id := fun.Run(`
    --sh 'echo foo > $out; chmod go+w $out'
    -o out={{ .storage }}/test_out
  `)
	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected success")
	}

	// this time around since the output file exists copyFile will be called
	// in storage.Put
	id = fun.Run(`
    --sh 'echo foo > $out; chmod go+w $out'
    -o out={{ .storage }}/test_out
  `)
	task = fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected success")
	}
}

func TestEmptyDir(t *testing.T) {
	tests.SetLogOutput(log, t)
	os.Mkdir(path.Join(fun.StorageDir, "test_in"), 0777)
	id := fun.Run(`
    --sh 'echo hello'
    -I in={{ .storage }}/test_in
    -O out={{ .storage }}/test_out
  `)
	task := fun.Wait(id)

	if task.State != tes.State_COMPLETE {
		t.Fatal("expected success")
	}

	found := false
	for _, log := range task.Logs[0].SystemLogs {
		if strings.Contains(log, "level='warning'") {
			found = true
		}
	}
	if !found {
		t.Fatal("Expected warning in system logs")
	}
}
