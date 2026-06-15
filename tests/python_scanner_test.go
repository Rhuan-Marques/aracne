package tests_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hasPython() bool {
	for _, name := range []string{"python3", "python"} {
		if err := exec.Command(name, "--version").Run(); err == nil {
			return true
		}
	}
	return false
}

func TestPythonScanDetectsBySetupPy(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "from mypkg.core import greet\n")
	writeFile(t, filepath.Join(dir, "mypkg", "core.py"), `
def greet(name):
    return "Hello " + name
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "search", "greet")
	if !strings.Contains(out, "greet") {
		t.Fatalf("expected greet in search, got:\n%s", out)
	}
}

func TestPythonScanDetectsByPyproject(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pyproject.toml"), `[build-system]
requires = ["setuptools"]
`)
	if err := os.MkdirAll(filepath.Join(dir, "src", "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "src", "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "src", "mypkg", "utils.py"), `
def util_func():
    return 42
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "search", "util_func")
	if !strings.Contains(out, "util_func") {
		t.Fatalf("expected util_func in search, got:\n%s", out)
	}
}

func TestPythonScanIgnoresTestFiles(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "core.py"), `
def production():
    return "ok"
`)
	writeFile(t, filepath.Join(dir, "mypkg", "test_core.py"), `
def test_production():
    assert production() == "ok"
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "search", "production")
	if !strings.Contains(out, "production") {
		t.Fatalf("expected production function in search, got:\n%s", out)
	}

	testOut, err := runLtp(t, dir, "search", "test_production")
	if err == nil && strings.Contains(testOut, "test_production") {
		t.Fatalf("expected test_production to be excluded, got:\n%s", testOut)
	}
}

func TestPythonScanIgnoresPycache(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "core.py"), `
def real_func():
    return 1
`)

	err := os.MkdirAll(filepath.Join(dir, "__pycache__", "mypkg"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "__pycache__", "mypkg", "cached.py"), `
def cached_func():
    return 2
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "search", "real_func")
	if !strings.Contains(out, "real_func") {
		t.Fatalf("expected real_func in search, got:\n%s", out)
	}

	cacheOut, err := runLtp(t, dir, "search", "cached_func")
	if err == nil && strings.Contains(cacheOut, "cached_func") {
		t.Fatalf("expected cached_func in __pycache__ to be excluded, got:\n%s", cacheOut)
	}
}

func TestPythonScanIgnoresVenv(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "core.py"), `
def app_func():
    return "app"
`)

	err := os.MkdirAll(filepath.Join(dir, "venv", "lib", "python3.10", "site-packages"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "venv", "lib", "python3.10", "site-packages", "dep.py"), `
def dep_func():
    return "dep"
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "search", "app_func")
	if !strings.Contains(out, "app_func") {
		t.Fatalf("expected app_func in search, got:\n%s", out)
	}

	venvOut, err := runLtp(t, dir, "search", "dep_func")
	if err == nil && strings.Contains(venvOut, "dep_func") {
		t.Fatalf("expected dep_func in venv to be excluded, got:\n%s", venvOut)
	}
}

func TestPythonClassInheritance(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "models.py"), `
class Base:
    def method(self):
        return "base"

class Child(Base):
    pass

class GrandChild(Child):
    pass
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "search", "Base")
	if !strings.Contains(out, "Base") {
		t.Fatalf("expected Base in search, got:\n%s", out)
	}
	out2 := mustRun(t, dir, "search", "Child")
	if !strings.Contains(out2, "Child") {
		t.Fatalf("expected Child in search, got:\n%s", out2)
	}
}

func TestPythonConstructorDetection(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "service.py"), `
class Service:
    def __init__(self, name):
        self.name = name

    def run(self):
        return self.name
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "search", "Service")
	if !strings.Contains(out, "Service") {
		t.Fatalf("expected Service in search, got:\n%s", out)
	}
	out2 := mustRun(t, dir, "search", "Service.run")
	if !strings.Contains(out2, "run") {
		t.Fatalf("expected Service.run in search, got:\n%s", out2)
	}
}

func TestPythonBodyCallWithTypeAnnotations(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "service.py"), `
class MyService:
    def run(self):
        return "running"

class Dispatcher:
    def dispatch(self, svc: MyService):
        return svc.run()
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("expected db to exist")
	}
	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	t.Logf("Python type annotation scan warnings:\n%s", warnOut)
}
