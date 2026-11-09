package logger

import (
	"fmt"
	"os"

	"google.golang.org/grpc/grpclog"
)

// SetGRPCLogger sets the global GRPC logger.
func SetGRPCLogger(l *Logger) {
	grpclog.SetLoggerV2(&grpclogger{log: l})
}

// Configure the GRPC logger to use the global logrus configuration
type grpclogger struct {
	log *Logger
}

func (g *grpclogger) Info(args ...interface{}) {
	g.log.Debug(fmt.Sprint(args...))
}
func (g *grpclogger) Infoln(args ...interface{}) {
	g.log.Debug(fmt.Sprintln(args...))
}
func (g *grpclogger) Infof(format string, args ...interface{}) {
	g.log.Debug(fmt.Sprintf(format, args...))
}
func (g *grpclogger) Warning(args ...interface{}) {
	g.log.Warn(fmt.Sprint(args...))
}
func (g *grpclogger) Warningln(args ...interface{}) {
	g.log.Warn(fmt.Sprintln(args...))
}
func (g *grpclogger) Warningf(format string, args ...interface{}) {
	g.log.Warn(fmt.Sprintf(format, args...))
}
func (g *grpclogger) Error(args ...interface{}) {
	g.log.Error(fmt.Sprint(args...))
}
func (g *grpclogger) Errorln(args ...interface{}) {
	g.log.Error(fmt.Sprintln(args...))
}
func (g *grpclogger) Errorf(format string, args ...interface{}) {
	g.log.Error(fmt.Sprintf(format, args...))
}
func (g *grpclogger) Fatal(args ...interface{}) {
	g.log.Error(fmt.Sprint(args...))
	os.Exit(1)
}
func (g *grpclogger) Fatalln(args ...interface{}) {
	g.log.Error(fmt.Sprintln(args...))
	os.Exit(1)
}
func (g *grpclogger) Fatalf(format string, args ...interface{}) {
	g.log.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
func (g *grpclogger) V(l int) bool {
	return true
}
