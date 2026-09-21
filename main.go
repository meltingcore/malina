package main

import (
	"embed"
	"log"

	"github.com/meltingcore/malina/internal/core"
	"github.com/meltingcore/malina/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[core.Job]("malina:job")
}

func main() {
	app := application.New(application.Options{
		Name:        "Malina",
		Description: "Live whole-device backup and restore for Raspberry Pi",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	app.RegisterService(application.NewService(desktop.NewService(app, core.NewEngine())))

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:              "Malina",
		Width:              1120,
		Height:             760,
		MinWidth:           760,
		MinHeight:          620,
		DisableResize:      false,
		ZoomControlEnabled: true,
		BackgroundColour:   application.NewRGB(234, 242, 239),
		URL:                "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
