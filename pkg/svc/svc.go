package svc

import (
	"errors"
	"os"

	"github.com/kardianos/service"
)

const ENV_WORKING_DIRECTORY = "SVC_WORKING_DIRECTORY"

var ControlActions = [7]string{"install", "uninstall", "start", "stop", "restart", "status", "run"}
var ControlLabels = [7]string{"安装", "卸载", "启动", "停止", "重启", "状态", "运行"}

func New() *Service {
	return &Service{}
}

type Service struct {
	Name             string
	DisplayName      string
	Description      string
	UserName         string
	Arguments        []string
	Executable       string
	Dependencies     []string
	WorkingDirectory string
	EnvVars          map[string]string
	Option           map[string]any
	ChRoot           string
	Run              func()
}

func (s *Service) build() (sc service.Service, err error) {
	if s.WorkingDirectory != "" {
		s.EnvVars["SVC_WORKING_DIRECTORY"] = s.WorkingDirectory
	}

	p := simpleProgram(func() {
		if workingDirectory := os.Getenv(ENV_WORKING_DIRECTORY); workingDirectory != "" {
			if service.Platform() == "windows-service" {
				if workingDirectory := os.Getenv(ENV_WORKING_DIRECTORY); workingDirectory != "" {
					os.MkdirAll(workingDirectory, 0755)
					os.Chdir(workingDirectory)
				}
			}
		}
		if s.Run != nil {
			s.Run()
		}
	})

	sc, err = service.New(&p, &service.Config{
		Name:             s.Name,
		DisplayName:      s.DisplayName,
		Description:      s.Description,
		UserName:         s.UserName,
		Arguments:        s.Arguments,
		Executable:       s.Executable,
		Dependencies:     s.Dependencies,
		WorkingDirectory: s.WorkingDirectory,
		ChRoot:           s.ChRoot, //not supported on Windows.
		Option:           s.Option, //not supported on Windows.
		EnvVars:          s.EnvVars,
	})

	return
}

func (s *Service) Control(command string) (msg string, err error) {
	var sc service.Service
	if sc, err = s.build(); err != nil {
		return
	}

	if command == "run" {
		err = sc.Run()
		return
	}

	status, se := sc.Status()

	if errors.Is(se, service.ErrNoServiceSystemDetected) {
		msg = "不支持该系统"
		err = se
		return
	}

	if errors.Is(se, service.ErrNameFieldRequired) {
		msg = "服务名称为空"
		err = se
		return
	}

	if errors.Is(se, service.ErrNotInstalled) { //服务未安装
		switch command {
		case "start", "stop", "restart", "uninstall", "status":
			msg = "服务未安装"
			err = se
			return
		}
	}

	if !errors.Is(se, service.ErrNotInstalled) && command == "install" {
		msg = "服务已安装"
		err = se
		return
	}

	if status == service.StatusUnknown {
		switch command {
		case "start", "stop", "restart", "uninstall", "status":
			msg = "服务状态未知"
			err = se
			return
		}
	}

	switch command {
	case "start":
		if status != service.StatusRunning {
			err = sc.Start()
		} else {
			msg = "服务已经在运行"
		}
	case "stop":
		if status != service.StatusStopped {
			err = sc.Stop()
		} else {
			msg = "服务已经在运行"
		}
	case "restart":
		err = sc.Restart()
	case "install":
		err = sc.Install()
	case "uninstall":
		err = sc.Uninstall()
	case "status":
		switch status {
		case service.StatusRunning:
			msg = "服务运行中"
		case service.StatusStopped:
			msg = "服务已停止"
		}
	}
	return
}

func (s *Service) Status() (status service.Status, err error) {
	var sc service.Service
	if sc, err = s.build(); err != nil {
		return
	}
	status, err = sc.Status()
	return
}

type simpleProgram func()

func (p simpleProgram) Start(service.Service) (err error) {
	if p != nil {
		go p()
	}
	return
}

func (p simpleProgram) Stop(service.Service) (err error) { return }

var _ service.Interface = simpleProgram(nil)
