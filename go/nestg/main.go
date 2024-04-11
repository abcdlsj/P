package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/abcdlsj/cr"
	"golang.org/x/mod/modfile"
)

type Identifier struct {
	Name   string
	Docker DockerFile
}

type DockerFile struct {
	Stages []Stage
	Execs  []string
}

type Stage struct {
	From   string
	Builds []string
	Expose string
}

func (s *Stage) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("FROM %s\n", s.From))
	for _, v := range s.Builds {
		sb.WriteString(fmt.Sprintf("%s\n", v))
	}

	if s.Expose != "" {
		sb.WriteString(fmt.Sprintf("EXPOSE %s\n", s.Expose))
	}

	return sb.String()
}

func (d *DockerFile) String() string {
	var sb strings.Builder

	for _, v := range d.Stages {
		sb.WriteString(v.String() + "\n")
	}

	sb.WriteString("CMD [")

	for i, v := range d.Execs {
		sb.WriteString(fmt.Sprintf("\"%s\"", v))
		if i != len(d.Execs)-1 {
			sb.WriteString(", ")
		}
	}

	sb.WriteString("]")

	return sb.String()
}

var (
	exposePort string
	ldflags    string
	imgname    string
	execFlags  string
	debug      = false
)

func genBuildCmd(binName, ldflags string) string {
	var sb strings.Builder
	sb.WriteString("RUN ")

	if ldflags == "" {
		ldflags = "-s -w"
	}

	sb.WriteString(fmt.Sprintf("go build -ldflags=\"%s\" -trimpath -o /dist/%s .", ldflags, binName))

	return sb.String()
}

func getBinaryName() string {
	dir, err := os.Getwd()
	if err != nil {
		return time.Now().Format("20060102150405") + "-" + "app"
	}

	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return path.Base(dir)
	}

	modf, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		panic(err)
	}

	mods := strings.Split(modf.Module.Mod.Path, "/")

	return mods[len(mods)-1]
}

func init() {
	flag.StringVar(&exposePort, "port", "", "port")
	flag.StringVar(&imgname, "img", "", "image name")
	flag.StringVar(&ldflags, "ldflags", "", "go build flags")
	flag.StringVar(&execFlags, "execflags", "", "exec flags")
	flag.BoolVar(&debug, "debug", false, "debug")
}

func getUserName() string {
	return os.Getenv("USER")
}

func main() {
	flag.Parse()

	binName := getBinaryName()

	ident := Identifier{
		Name: "golang:alpine",
		Docker: DockerFile{
			Stages: []Stage{
				{
					From: "golang:alpine AS builder",
					Builds: vec(
						"RUN apk add --no-cache build-base",
						"RUN apk --no-cache add ca-certificates",
						"WORKDIR /build",
						"COPY . .",
						genBuildCmd(binName, ldflags),
						"RUN ldd /dist/"+binName+" | tr -s [:blank:] '\\n' | grep ^/ | xargs -I % install -D % /dist/%",
						"RUN ln -s ld-musl-x86_64.so.1 /dist/lib/libc.musl-x86_64.so.1",
					),
				},
				{
					From: "scratch",
					Builds: vec(
						"COPY --from=builder /dist /",
						"COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/",
					),
					Expose: exposePort,
				},
			},
			Execs: []string{
				"/" + binName,
			},
		},
	}

	execFlags := strings.Split(execFlags, " ")
	for _, v := range execFlags {
		if v == "" {
			continue
		}
		ident.Docker.Execs = append(ident.Docker.Execs, fmt.Sprintf("\"%s\"", v))
	}

	if imgname == "" {
		imgname = getUserName() + "/" + binName + ":" + time.Now().Format("20060102150405")[8:]
	}

	fmt.Printf("Identifier: %s, Binary: %s, Image: %s\n", cr.PLBlue(ident.Name), cr.PLBlue(binName), cr.PLBlue(imgname))

	tmpf, err := os.CreateTemp("", fmt.Sprintf("%s-*.dockerfile", binName))
	if err != nil {
		fmt.Printf("Temp file create error: %s\n", cr.PLRed(err.Error()))
		return
	}

	fmt.Printf("Temp dockerfile: %s\n", cr.PLYellow(tmpf.Name()))

	tmpf.WriteString(ident.Docker.String())
	defer os.Remove(tmpf.Name())

	fmt.Printf("Dockerfile content:\n%s\n", cr.PLYellow(ident.Docker.String()))

	cmd := exec.Command("docker", "build", "-t", imgname, "-f", tmpf.Name(), ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Build image error: %s\n", cr.PLRed(err.Error()))
		return
	}

	if debug {
		fmt.Printf("Run: %s\n", cr.PLYellow("docker run -it --rm "+imgname))
		return
	}

	if exposePort != "" {
		fmt.Printf("Run: %s\n", cr.PLYellow("docker run -d --rm -p "+"<hostport>:"+exposePort+" "+imgname))
		return
	}

	fmt.Printf("Run: %s\n", cr.PLYellow("docker run -d --rm "+imgname))
}

func vec(s ...string) []string {
	return s
}
