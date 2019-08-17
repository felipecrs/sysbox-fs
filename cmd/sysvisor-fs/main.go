package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nestybox/sysvisor-fs/domain"
	"github.com/nestybox/sysvisor-fs/fuse"
	"github.com/nestybox/sysvisor-fs/handler"
	"github.com/nestybox/sysvisor-fs/ipc"
	"github.com/nestybox/sysvisor-fs/nsenter"
	"github.com/nestybox/sysvisor-fs/state"
	"github.com/nestybox/sysvisor-fs/sysio"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

// TODO: Improve one-liner description.
const (
	usage = `sysvisor file-system

sysvisor-fs is a daemon that provides enhanced file-system capabilities to
sysvisor-runc component.
`
)

//
// Sysvisorfs signal handler goroutine.
//
func signalHandler(signalChan chan os.Signal, fs domain.FuseService) {

	s := <-signalChan

	switch s {

	// TODO: Handle SIGHUP differently -- e.g. re-read sysvisorfs conf file
	case syscall.SIGHUP:
		logrus.Warn("sysvisor-fs caught signal: SIGHUP")

	case syscall.SIGSEGV:
		logrus.Warn("sysvisor-fs caught signal: SIGSEGV")

	case syscall.SIGINT:
		logrus.Warn("sysvisor-fs caught signal: SIGTINT")

	case syscall.SIGTERM:
		logrus.Warn("sysvisor-fs caught signal: SIGTERM")

	case syscall.SIGQUIT:
		logrus.Warn("sysvisor-fs caught signal: SIGQUIT")

	default:
		logrus.Warn("sysvisor-fs caught unknown signal")
	}

	logrus.Warn(
		"Unmounting sysvisor-fs from mountpoint ",
		fs.MountPoint(),
		". Exitting...",
	)
	fs.Unmount()

	// Deferring exit() to allow FUSE to dump unnmount() logs
	time.Sleep(2)

	os.Exit(0)
}

//
// Sysvisor-fs main function
//
func main() {

	app := cli.NewApp()
	app.Name = "sysvisor-fs"
	app.Usage = usage

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "mountpoint",
			Value: "/var/lib/sysvisorfs",
			Usage: "mount-point location",
		},
		cli.StringFlag{
			Name:  "log",
			Value: "/dev/stdout",
			Usage: "log file path",
		},
		cli.StringFlag{
			Name:  "log-level",
			Value: "info",
			Usage: "log categories to include (debug, info, warning, error, fatal)",
		},
		cli.IntFlag{
			Name:  "dentry-cache-timeout, t",
			Value: fuse.DentryCacheTimeout,
			Usage: "dentry-cache-timeout timer in minutes",
			Destination: &fuse.DentryCacheTimeout,
		},
	}

	// Nsenter command to allow 'rexec' functionality.
	app.Commands = []cli.Command{
		{
			Name:  "nsenter",
			Usage: "Execute action within container namespaces",
			Action: func(c *cli.Context) error {
				nsenter.Init()
				return nil
			},
		},
	}

	// Define 'debug' and 'log' settings.
	app.Before = func(ctx *cli.Context) error {

		// Create/set the log-file destination.
		if path := ctx.GlobalString("log"); path != "" {
			f, err := os.OpenFile(
				path,
				os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC,
				0666,
			)
			if err != nil {
				logrus.Fatalf("Error opening log file %v: %v", path, err)
				return err
			}

			// Set a proper logging formatter.
			logrus.SetFormatter(&logrus.TextFormatter{
				ForceColors: true,
				TimestampFormat : "2006-01-02 15:04:05",
				FullTimestamp: true,
			})
			logrus.SetOutput(f)
			log.SetOutput(f)
		}

		// Set desired log-level.
		if logLevel := ctx.GlobalString("log-level"); logLevel != "" {
			switch logLevel {
			case "debug":
				// Following instruction is to have Bazil's fuze-lib logs being
				// included into sysvisor-fs' log stream.
				flag.Set("fuse.debug", "true")
				logrus.SetLevel(logrus.DebugLevel)
			case "info":
				logrus.SetLevel(logrus.InfoLevel)
			case "warning":
				logrus.SetLevel(logrus.WarnLevel)
			case "error":
				logrus.SetLevel(logrus.ErrorLevel)
			case "fatal":
				logrus.SetLevel(logrus.FatalLevel)
			default:
				logrus.Fatalf("'%v' log-level option not recognized", logLevel)
			}
		} else {
			// Set 'info' as our default log-level.
			logrus.SetLevel(logrus.InfoLevel)
		}

		return nil
	}

	// Sysvisor-fs main-loop execution.
	app.Action = func(ctx *cli.Context) error {

		// Initialize sysvisor-fs' services.
		var containerStateService = state.NewContainerStateService()
		var nsenterService = nsenter.NewNSenterService()
		var ioService = sysio.NewIOService(sysio.IOFileService)

		var handlerService = handler.NewHandlerService(
			handler.DefaultHandlers,
			containerStateService,
			nsenterService,
			ioService)

		var ipcService = ipc.NewIpcService(containerStateService, ioService)
		ipcService.Init()

		var fuseService = fuse.NewFuseService(
			"/",
			ctx.GlobalString("mountpoint"),
			ioService,
			handlerService)

		// Launch signal-handler to ensure mountpoint is properly unmounted
		// during shutdown.
		var signalChan = make(chan os.Signal)
		signal.Notify(
			signalChan,
			syscall.SIGHUP,
			syscall.SIGINT,
			syscall.SIGTERM,
			syscall.SIGSEGV,
			syscall.SIGQUIT)
		go signalHandler(signalChan, fuseService)

		// Initiate sysvisor-fs' FUSE service.
		if err := fuseService.Run(); err != nil {
			logrus.Fatal(err)
		}

		return nil
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
