// Command release builds and verifies one platform's Cordis distribution.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Cyenoch/cordis/packagehost"
)

func main() {
	version := flag.String("version", "", "release tag, including v prefix")
	output := flag.String("output", "dist", "output directory")
	flag.Parse()
	if err := build(*version, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(version, output string) (result error) {
	if !strings.HasPrefix(version, "v") {
		return fmt.Errorf("version must be an explicit v-prefixed tag")
	}
	if err := packagehost.ValidateVersion(strings.TrimPrefix(version, "v")); err != nil {
		return err
	}
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64":
	default:
		return fmt.Errorf("unsupported release platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	revision := strings.TrimSpace(string(commit))
	work, err := os.MkdirTemp("", "cordis-release-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(work)) }()
	binary := "cordis"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-X main.releaseVersion="+version+" -X main.releaseCommit="+revision,
		"-o", filepath.Join(work, binary), "./cmd/cordis")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return err
	}
	license, err := os.ReadFile("LICENSE")
	if err != nil {
		return err
	}
	readme := fmt.Sprintf("Cordis %s\nCommit: %s\nPlatform: %s/%s\n\nRun cordis version and cordis help.\nNative plugins run with your user permissions.\n", version, revision, runtime.GOOS, runtime.GOARCH)
	if err := os.WriteFile(filepath.Join(work, "LICENSE"), license, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "RELEASE.txt"), []byte(readme), 0600); err != nil {
		return err
	}
	extension := ".tar.gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
	}
	base := fmt.Sprintf("cordis_%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, extension)
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	archive := filepath.Join(output, base)
	if err := archiveFiles(archive, work, []string{binary, "LICENSE", "RELEASE.txt"}); err != nil {
		return err
	}
	if err := verifyArchive(archive, binary, version, revision); err != nil {
		return err
	}
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	checksum := fmt.Sprintf("%x  %s\n", hash.Sum(nil), base)
	if err := os.WriteFile(archive+".sha256", []byte(checksum), 0644); err != nil {
		return err
	}
	fmt.Print(checksum)
	return nil
}

func archiveFiles(destination, source string, names []string) (result error) {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, output.Close())
		if result != nil {
			result = errors.Join(result, os.Remove(destination))
		}
	}()
	var zipper *zip.Writer
	var tarball *tar.Writer
	if strings.HasSuffix(destination, ".zip") {
		zipper = zip.NewWriter(output)
		defer func() { result = errors.Join(result, zipper.Close()) }()
	} else {
		compressed := gzip.NewWriter(output)
		defer func() { result = errors.Join(result, compressed.Close()) }()
		tarball = tar.NewWriter(compressed)
		defer func() { result = errors.Join(result, tarball.Close()) }()
	}
	for _, name := range names {
		file, err := os.Open(filepath.Join(source, name))
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			return errors.Join(err, file.Close())
		}
		mode := int64(0644)
		if name == "cordis" || name == "cordis.exe" {
			mode = 0755
		}
		var writer io.Writer
		if zipper != nil {
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			header.SetMode(os.FileMode(mode))
			writer, err = zipper.CreateHeader(header)
		} else {
			err = tarball.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: info.Size(), ModTime: time.Unix(0, 0)})
			writer = tarball
		}
		if err == nil {
			_, err = io.Copy(writer, file)
		}
		if err = errors.Join(err, file.Close()); err != nil {
			return err
		}
	}
	return nil
}

// verifyArchive executes the binary extracted from the distribution, not the
// build directory, so packaging and execution permissions are checked together.
func verifyArchive(archive, binary, version, revision string) (result error) {
	work, err := os.MkdirTemp("", "cordis-release-check-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(work)) }()
	write := func(name string, mode os.FileMode, reader io.Reader) error {
		if name != binary && name != "LICENSE" && name != "RELEASE.txt" {
			return fmt.Errorf("unexpected release file %q", name)
		}
		file, err := os.OpenFile(filepath.Join(work, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(file, reader)
		return errors.Join(err, file.Close())
	}
	if strings.HasSuffix(archive, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, z.Close()) }()
		for _, file := range z.File {
			reader, err := file.Open()
			if err != nil {
				return err
			}
			err = write(file.Name, file.Mode(), reader)
			if err = errors.Join(err, reader.Close()); err != nil {
				return err
			}
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, file.Close()) }()
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, compressed.Close()) }()
		reader := tar.NewReader(compressed)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if err := write(header.Name, os.FileMode(header.Mode), reader); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"LICENSE", "RELEASE.txt"} {
		info, err := os.Stat(filepath.Join(work, name))
		if err != nil || info.Size() == 0 {
			return fmt.Errorf("release lacks %s", name)
		}
	}
	command := exec.Command(filepath.Join(work, binary), "version")
	value, err := command.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(value), "cordis "+version+" ") ||
		!strings.Contains(string(value), "release.commit "+revision+"\n") {
		return fmt.Errorf("release version verification: %v: %s", err, value)
	}
	command = exec.Command(filepath.Join(work, binary), "help")
	if value, err := command.CombinedOutput(); err != nil || !strings.Contains(string(value), "cordis") {
		return fmt.Errorf("release help verification: %v: %s", err, value)
	}
	return nil
}
