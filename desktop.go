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
		command = exec.CommandContext(ctx, "/usr/bin/osascript", "-e", "POSIX path of (choose "+kind+" with prompt \"Scegli i pesi per Jase Tunnel\")")
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $dialog=New-Object Windows.Forms.OpenFileDialog; $dialog.Title='Scegli i pesi per Jase Tunnel'; $dialog.Filter='Modelli GGUF (*.gguf)|*.gguf'; if($dialog.ShowDialog() -eq 'OK'){Write-Output $dialog.FileName}`
		if folder {
			script = `Add-Type -AssemblyName System.Windows.Forms; $dialog=New-Object Windows.Forms.FolderBrowserDialog; $dialog.Description='Scegli la cartella dei pesi per Jase Tunnel'; if($dialog.ShowDialog() -eq 'OK'){Write-Output $dialog.SelectedPath}`
		}
		command = exec.CommandContext(ctx, "powershell.exe", "-STA", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new();"+script)
	default:
		if path, err := exec.LookPath("zenity"); err == nil {
			args := []string{"--file-selection", "--title=Scegli i pesi per Jase Tunnel"}
			if folder {
				args = append(args, "--directory")
			} else {
				args = append(args, "--file-filter=Modelli GGUF | *.gguf")
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
		return "", errors.New("Il selettore file richiede Zenity o KDialog su Linux. Puoi importare il modello nel tuo motore locale e Jase Tunnel lo rileverà automaticamente.")
	}
	hideChildConsole(command)
	output, err := command.Output()
	if err != nil {
		return "", errors.New("Selezione annullata.")
	}
	return strings.TrimSpace(string(output)), nil
}

var page = template.Must(template.New("status").Parse(`<!doctype html><html lang="it"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Jase Tunnel</title><style>body{font:16px system-ui;background:#faf9f6;color:#252d28;max-width:620px;margin:8vh auto;padding:24px}h1{color:#e6632e}p{line-height:1.6}button{font:inherit;background:#fff;border:1px solid #d8dcd7;border-radius:12px;padding:12px 18px;margin:8px 8px 8px 0;cursor:pointer}#message{min-height:48px}</style><h1>Jase Tunnel</h1><h2>La tua LLM locale, sui siti che la accettano.</h2><p>L’app è aperta in background. Quando una pagina compatibile si annuncia, Jase Tunnel te lo segnala: il collegamento parte solo dopo la tua conferma e dura al massimo 30 minuti.</p><p id="message" role="status">Verifica dei modelli locali…</p><button id="file">Aggiungi file GGUF</button><button id="folder">Aggiungi cartella modello</button><button id="quit">Chiudi Jase Tunnel</button><script nonce="{{.Nonce}}">const message=document.getElementById('message');async function call(path,body={}){const r=await fetch('/v1/'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});const data=await r.json();if(!r.ok)throw Error(data.error?.message||'Operazione non riuscita');return data}async function status(){try{const r=await fetch('/v1/status');const d=await r.json();message.textContent=d.modelCount+' modelli locali rilevati. Apri Ollama o LM Studio per rendere disponibili i suoi modelli.'}catch{message.textContent='L’app è stata chiusa.'}}for(const id of ['file','folder'])document.getElementById(id).onclick=async()=>{message.textContent='Scegli il modello nella finestra del computer.';try{await call('add-model',{folder:id==='folder'});await status()}catch(e){message.textContent=e.message}};document.getElementById('quit').onclick=()=>call('quit').then(()=>message.textContent='App chiusa.');status();</script></html>`))

func (a *application) statusPage(w http.ResponseWriter) {
	nonce := randomSecret()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-%s'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'", nonce))
	_ = page.Execute(w, map[string]string{"Nonce": nonce})
}
