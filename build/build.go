package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/mod/modfile"
)

func main() {
	var (
		version   string
		buildTime = time.Now().Format(time.RFC3339)
	)

	data, _ := os.ReadFile("go.mod")
	f, _ := modfile.Parse("go.mod", data, nil)

	const clashMod = "github.com/Dreamacro/clash"
	r, _ := lo.Find(f.Require, func(it *modfile.Require) bool { return it.Mod.Path == clashMod })
	if r != nil {
		version = r.Mod.Version
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	ldflags := fmt.Sprintf(`-s -w -X %s/constant.Version=%s -X %s/constant.BuildTime=%s`, clashMod, version, clashMod, buildTime)

	localVersion := gitTag(ctx)
	if localVersion != "" {
		ldflags += fmt.Sprintf(" -X main.Version=%s", localVersion)
	}

	buildIt := func(goos, goarch string) {
		log.Printf("build: %s-%s-%s", goos, goarch, localVersion)
		output := fmt.Sprintf("bin/hlash-%s-%s", goos, goarch)
		if goos == "windows" {
			output += ".exe"
		}

		name, args := shell(`go build -trimpath -v`)
		args = append(args, `-ldflags`, ldflags, `-o`, output, `./`)

		c := exec.CommandContext(ctx, name, args...)
		c.Stderr = os.Stderr
		c.Stdout = os.Stdout
		c.Stdin = os.Stdin
		c.Env = append(
			os.Environ(),
			"CGO_ENABLED=0",
			fmt.Sprintf("GOOS=%s", goos),
			fmt.Sprintf("GOARCH=%s", goarch),
		)

		if err := c.Run(); err != nil {
			log.Fatalf("build: %v", err)
		}

		if err := upx(ctx, output); err != nil {
			log.Fatalf("upx: %v", err)
		}

		if err := gz(output); err != nil {
			log.Fatalf("gzip: %v", err)
		}
	}

	goEnvs := [][2]string{
		{"windows", "amd64"},
		{"windows", "arm64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"linux", "arm"},
		{"linux", "mips"},
		{"linux", "mipsle"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
	}

	for _, goEnv := range goEnvs {
		buildIt(goEnv[0], goEnv[1])
	}

	// listGOOS := []string{"windows", "darwin", "linux"}
	// listGOARCH := []string{"amd64", "arm64", "arm"}
	// for _, goos := range listGOOS {
	// 	for _, goarch := range listGOARCH {
	// 		buildIt(goos, goarch)
	// 	}
	// }
}

func upx(ctx context.Context, fn string) (err error) {
	//upx之后运行内存占用高出10M+
	// 	<-cmd.New("upx", fn).With(cmdOpts...).With(cacheErr(&err)).Start(ctx).Done()
	return
}

func gz(fn string) (err error) {
	var src, dst *os.File
	if src, err = os.Open(fn); err != nil {
		return
	}

	if dst, err = os.Create(fn + ".gz"); err != nil {
		src.Close()
		return
	}
	defer dst.Close()

	gz := gzip.NewWriter(dst)
	defer gz.Close()

	_, err = io.Copy(gz, src)
	src.Close()
	os.Remove(fn)
	return
}

func gitTag(ctx context.Context) (version string) {
	name, args := shell(`git rev-list --tags --max-count=1`)
	d, e := exec.CommandContext(ctx, name, args...).Output()
	if e != nil {
		log.Printf("%v", e)
	}

	if hash := string(d); hash != "" {
		name, args := shell(`git describe --tags ` + hash)
		v, e := exec.CommandContext(ctx, name, args...).Output()
		if e != nil {
			log.Printf("%v", e)
		}
		version = string(v)
	}
	return
}

func shell(script string) (name string, args []string) {
	if args = strings.Fields(script); len(args) > 0 {
		name, args = args[0], args[1:]
	}
	return
}
