package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type browser struct{ Name, Executable string }

func browsers() []browser {
	result := []browser{}
	home, _ := os.UserHomeDir()
	var candidates []browser
	switch runtime.GOOS {
	case "darwin":
		for _, name := range []string{"Opera", "Safari", "Google Chrome", "Firefox", "Brave Browser", "Microsoft Edge", "Vivaldi"} {
			candidates = append(candidates, browser{name, "/Applications/" + name + ".app"}, browser{name, filepath.Join(home, "Applications", name+".app")})
		}
	case "windows":
		for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)")} {
			if base == "" {
				continue
			}
			for _, item := range []browser{{"Opera", "Programs/Opera/launcher.exe"}, {"Opera", "Opera/launcher.exe"}, {"Chrome", "Google/Chrome/Application/chrome.exe"}, {"Firefox", "Mozilla Firefox/firefox.exe"}, {"Edge", "Microsoft/Edge/Application/msedge.exe"}, {"Brave", "BraveSoftware/Brave-Browser/Application/brave.exe"}, {"Vivaldi", "Vivaldi/Application/vivaldi.exe"}} {
				candidates = append(candidates, browser{item.Name, filepath.Join(base, filepath.FromSlash(item.Executable))})
			}
		}
	default:
		for _, item := range []browser{{"Opera", "opera"}, {"Firefox", "firefox"}, {"Chromium", "chromium"}, {"Chrome", "google-chrome"}, {"Brave", "brave-browser"}, {"Vivaldi", "vivaldi"}, {"Edge", "microsoft-edge"}} {
			if path, err := exec.LookPath(item.Executable); err == nil {
				candidates = append(candidates, browser{item.Name, path})
			}
		}
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate.Executable); err == nil && !seen[candidate.Name] {
			seen[candidate.Name] = true
			result = append(result, candidate)
		}
	}
	return result
}
func (a *application) openJase() {
	list := browsers()
	if len(list) == 0 {
		return
	}
	a.mu.RLock()
	preferred := a.settings.Browser
	a.mu.RUnlock()
	chosen := ""
	for _, b := range list {
		if b.Name == preferred {
			chosen = b.Name
		}
	}
	if chosen == "" && len(list) == 1 {
		chosen = list[0].Name
	}
	if chosen == "" {
		names := []string{}
		for _, b := range list {
			names = append(names, b.Name)
		}
		chosen = chooseBrowser(a.ctx, names)
	}
	for _, b := range list {
		if b.Name != chosen {
			continue
		}
		var command *exec.Cmd
		if runtime.GOOS == "darwin" {
			command = exec.Command("/usr/bin/open", "-a", b.Executable, a.siteURL())
		} else {
			command = exec.Command(b.Executable, a.siteURL())
		}
		if command.Start() == nil {
			go command.Wait()
			a.mu.Lock()
			a.settings.Browser = b.Name
			_ = a.saveLocked()
			a.mu.Unlock()
		}
		return
	}
}
func chooseBrowser(ctx context.Context, names []string) string {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		quoted := []string{}
		for _, name := range names {
			quoted = append(quoted, strconv.Quote(name))
		}
		script := "set result to choose from list {" + strings.Join(quoted, ",") + "} with title \"Jase Tunnel\" with prompt \"Apri Jase con quale browser?\"\nif result is false then return \"\"\nreturn item 1 of result"
		command = exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script)
	case "windows":
		items := []string{}
		for _, name := range names {
			items = append(items, "'"+strings.ReplaceAll(name, "'", "''")+"'")
		}
		script := `Add-Type -AssemblyName System.Windows.Forms; $form=New-Object Windows.Forms.Form; $form.Text='Jase Tunnel'; $form.Width=350; $form.Height=170; $label=New-Object Windows.Forms.Label; $label.Text='Apri Jase con quale browser?'; $label.SetBounds(20,15,300,25); $list=New-Object Windows.Forms.ComboBox; $list.SetBounds(20,45,290,25); $list.DropDownStyle='DropDownList'; $list.Items.AddRange(@(` + strings.Join(items, ",") + `)); $list.SelectedIndex=0; $button=New-Object Windows.Forms.Button; $button.Text='Apri Jase'; $button.SetBounds(200,80,110,30); $button.DialogResult='OK'; $form.Controls.AddRange(@($label,$list,$button)); $form.AcceptButton=$button; if($form.ShowDialog() -eq 'OK'){Write-Output $list.SelectedItem}`
		command = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new();"+script)
	default:
		if path, err := exec.LookPath("zenity"); err == nil {
			args := []string{"--list", "--title=Jase Tunnel", "--text=Apri Jase con quale browser?", "--column=Browser"}
			args = append(args, names...)
			command = exec.CommandContext(ctx, path, args...)
		} else if path, err := exec.LookPath("kdialog"); err == nil {
			args := []string{"--title", "Jase Tunnel", "--combobox", "Apri Jase con quale browser?"}
			args = append(args, names...)
			command = exec.CommandContext(ctx, path, args...)
		}
	}
	if command == nil {
		return ""
	}
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
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
		command = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new();"+script)
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
		return "", errors.New("Il selettore file richiede Zenity o KDialog su Linux. Puoi importare il modello nel tuo motore locale e Jase lo rileverà automaticamente.")
	}
	output, err := command.Output()
	if err != nil {
		return "", errors.New("Selezione annullata.")
	}
	return strings.TrimSpace(string(output)), nil
}

var page = template.Must(template.New("status").Parse(`<!doctype html><html lang="it"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Jase Tunnel</title><style>body{font:16px system-ui;background:#faf9f6;color:#252d28;max-width:620px;margin:8vh auto;padding:24px}h1{color:#e6632e}p{line-height:1.6}button{font:inherit;background:#fff;border:1px solid #d8dcd7;border-radius:12px;padding:12px 18px;margin:8px 8px 8px 0;cursor:pointer}#message{min-height:48px}</style><h1>Jase Tunnel</h1><h2>Il tuo modello, gli strumenti di Jase.</h2><p>L’app è aperta in background. Nella scheda di Jase scegli «Usa il mio modello»: il collegamento è automatico e non richiede un account.</p><p id="message" role="status">Verifica dei modelli locali…</p><button id="open">Apri Jase nel browser che scegli</button><button id="file">Aggiungi file GGUF</button><button id="folder">Aggiungi cartella modello</button><button id="quit">Chiudi Jase Tunnel</button><script nonce="{{.Nonce}}">const message=document.getElementById('message');async function call(path,body={}){const r=await fetch('/v1/'+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});const data=await r.json();if(!r.ok)throw Error(data.error?.message||'Operazione non riuscita');return data}async function status(){try{const r=await fetch('/v1/status');const d=await r.json();message.textContent=d.modelCount+' modelli locali rilevati. Apri Ollama o LM Studio per rendere disponibili i suoi modelli.'}catch{message.textContent='L’app è stata chiusa.'}}document.getElementById('open').onclick=()=>call('open-jase').catch(e=>message.textContent=e.message);for(const id of ['file','folder'])document.getElementById(id).onclick=async()=>{message.textContent='Scegli il modello nella finestra del computer.';try{await call('add-model',{folder:id==='folder'});await status()}catch(e){message.textContent=e.message}};document.getElementById('quit').onclick=()=>call('quit').then(()=>message.textContent='App chiusa.');status();</script></html>`))

func (a *application) statusPage(w http.ResponseWriter) {
	nonce := randomSecret()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-%s'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'", nonce))
	_ = page.Execute(w, map[string]string{"Nonce": nonce})
}

// The page the app opens: Jase's AI mode on the configured site (--site).
func (a *application) siteURL() string {
	site := a.site
	if site == "" {
		site = "https://jaseengine.it"
	}
	return site + "/?mode=ai"
}
