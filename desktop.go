package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func pickModel(ctx context.Context, folder bool) (string, error) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		kind := "file"
		if folder {
			kind = "folder"
		}
		command = exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "POSIX path of (choose "+kind+" with prompt \"Choose weights for Jase Tunnel\")")
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $dialog=New-Object Windows.Forms.OpenFileDialog; $dialog.Title='Choose weights for Jase Tunnel'; $dialog.Filter='GGUF models (*.gguf)|*.gguf'; if($dialog.ShowDialog() -eq 'OK'){Write-Output $dialog.FileName}`
		if folder {
			script = `Add-Type -AssemblyName System.Windows.Forms; $dialog=New-Object Windows.Forms.FolderBrowserDialog; $dialog.Description='Choose a weights directory for Jase Tunnel'; if($dialog.ShowDialog() -eq 'OK'){Write-Output $dialog.SelectedPath}`
		}
		command = exec.CommandContext(ctx, "powershell.exe", "-STA", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new();"+script)
	default:
		if path, err := exec.LookPath("zenity"); err == nil {
			args := []string{"--file-selection", "--title=Choose weights for Jase Tunnel"}
			if folder {
				args = append(args, "--directory")
			} else {
				args = append(args, "--file-filter=GGUF models | *.gguf")
			}
			command = exec.CommandContext(ctx, path, args...)
		} else if path, err := exec.LookPath("kdialog"); err == nil {
			args := []string{"--getopenfilename", os.Getenv("HOME"), "*.gguf"}
			if folder {
				args = []string{"--getexistingdirectory", os.Getenv("HOME")}
			}
			command = exec.CommandContext(ctx, path, args...)
		}
	}
	if command == nil {
		return "", errors.New("The file chooser requires Zenity or KDialog on Linux. You can import the model into your local engine and Jase Tunnel will detect it automatically.")
	}
	hideChildConsole(command)
	output, err := command.Output()
	if err != nil {
		return "", errors.New("Selection cancelled.")
	}
	return strings.TrimSpace(string(output)), nil
}

var page = template.Must(template.New("status").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Jase Tunnel</title><style>body{font:16px system-ui;background:#faf9f6;color:#252d28;max-width:620px;margin:8vh auto;padding:24px}h1{color:#e6632e}p{line-height:1.6}button{font:inherit;background:#fff;border:1px solid #d8dcd7;border-radius:12px;padding:12px 18px;margin:8px 8px 8px 0;cursor:pointer}#message{min-height:48px}</style><h1>Jase Tunnel</h1><h2>Your local LLM, on websites that support it.</h2><p>The app is running in the background. When a compatible page announces itself, Jase Tunnel notifies you. Connections start only after your confirmation and last up to 30 minutes.</p><p id="message" role="status">Checking local models…</p><button id="file">Add GGUF file</button><button id="folder">Add model directory</button><button id="quit">Quit Jase Tunnel</button><script nonce="{{.Nonce}}">const message=document.getElementById('message');async function call(path,body={}){const r=await fetch('/v1/'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});const data=await r.json();if(!r.ok)throw Error(data.error?.message||'Operation failed');return data}async function status(){try{const r=await fetch('/v1/status');const d=await r.json();message.textContent=d.modelCount+' local models detected. Open Ollama or LM Studio to make its models available.'}catch{message.textContent='The app has closed.'}}for(const id of ['file','folder'])document.getElementById(id).onclick=async()=>{message.textContent='Choose a model in the app window.';try{await call('add-model',{folder:id==='folder'});await status()}catch(e){message.textContent=e.message}};document.getElementById('quit').onclick=()=>call('quit').then(()=>message.textContent='App closed.');status();</script></html>`))

func (a *application) statusPage(w http.ResponseWriter) {
	nonce := randomSecret()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-%s'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'", nonce))
	_ = page.Execute(w, map[string]string{"Nonce": nonce})
}
