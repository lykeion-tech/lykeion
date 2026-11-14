package envs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const FingerprintVersion = "coresearch-env-v1"
const inventoryLimit = 4 << 20

// Fingerprint inventories every prepared language exposed to recorded processes.
// The runner must enforce the same read-only sandbox as execution.
func (s *Study) Fingerprint(ctx context.Context) (string, error) {
	if s.Run == nil {
		return "", errNoRunner
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	environment, err := s.Environment(ctx)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	add := func(name string, data []byte) {
		fmt.Fprintf(hash, "%d:%s%d:", len(name), name, len(data))
		hash.Write(data)
	}
	add("format", []byte(FingerprintVersion))
	command := func(label string, env []string, name string, args ...string) error {
		data, err := s.Run(ctx, env, s.Dir, name, args...)
		if err != nil {
			return fmt.Errorf("inventory %s: %w", label, err)
		}
		if len(data) > inventoryLimit {
			return fmt.Errorf("inventory %s exceeds 4 MiB", label)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		sort.Strings(lines)
		add(label, []byte(strings.Join(lines, "\n")))
		return nil
	}
	file := func(label, path string) error {
		data, err := s.Run(ctx, environment, s.Dir, "/bin/sh", "-c", `if [ -e "$1" ] || [ -L "$1" ]; then printf 'present\n'; head -c 4194305 "$1"; else printf 'absent\n'; fi`, "inventory", path)
		if err != nil {
			return fmt.Errorf("inventory %s: %w", label, err)
		}
		if len(data) > inventoryLimit {
			return fmt.Errorf("inventory %s exceeds 4 MiB", label)
		}
		add(label, data)
		return ctx.Err()
	}
	if err = file("project-conda-lock", filepath.Join(s.Dir, "conda/lock.txt")); err != nil {
		return "", err
	}
	for _, lang := range []string{Python, R, Julia} {
		ready := s.Bases.Ready(lang)
		add(lang+"-ready", []byte(fmt.Sprint(ready)))
		if !ready {
			continue
		}
		if err = s.Ready(lang); err != nil {
			return "", err
		}
		if err = file(lang+"-base-lock", filepath.Join(s.Bases.baseDir(lang), "lock.txt")); err != nil {
			return "", err
		}
		switch lang {
		case Python:
			for _, entry := range [][2]string{{"python-base", filepath.Join(s.Bases.Dir, "python/bin/python")}, {"python-project", filepath.Join(s.Dir, "python/bin/python")}} {
				if err = command(entry[0], environment, entry[1], "-c", pythonInventory, s.Dir); err != nil {
					return "", err
				}
			}
		case R:
			if err = file("r-profile", filepath.Join(s.Dir, "r/Rprofile")); err != nil {
				return "", err
			}
			if err = command("r-packages", environment, filepath.Join(s.Bases.Dir, "r/bin/Rscript"), "-e", rInventory); err != nil {
				return "", err
			}
		case Julia:
			for _, entry := range [][2]string{{"julia-base", s.Bases.baseDir(Julia)}, {"julia-project", filepath.Join(s.Dir, "julia")}} {
				for _, name := range []string{"Project.toml", "Manifest.toml"} {
					if err = file(entry[0]+"-"+name, filepath.Join(entry[1], name)); err != nil {
						return "", err
					}
				}
				env := append([]string(nil), environment...)
				for i, value := range env {
					if strings.HasPrefix(value, "JULIA_PROJECT=") {
						env[i] = "JULIA_PROJECT=" + entry[1]
					}
				}
				if err = command(entry[0]+"-packages", env, filepath.Join(s.Bases.juliaBin(), "julia"), "--startup-file=no", "--history-file=no", "--compiled-modules=no", "-e", juliaInventory); err != nil {
					return "", err
				}
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

const pythonInventory = `import sys, json, pathlib, importlib.metadata as m
print(json.dumps(["interpreter",sys.version,sys.implementation.name,sys.prefix,sys.base_prefix]))
for d in sorted(m.distributions(), key=lambda d: (d.metadata.get("Name", ""),d.version,str(d.locate_file("")))):
 print(json.dumps(["distribution",d.metadata.get("Name", ""),d.version,str(d.locate_file(""))],sort_keys=True))
for root in sorted(set([sys.prefix,sys.base_prefix,str(pathlib.Path(sys.argv[1])/"conda")])):
 for p in sorted((pathlib.Path(root)/"conda-meta").glob("*.json")):
  v=json.loads(p.read_text()); print(json.dumps(["conda",root,v.get("name"),v.get("version"),v.get("build"),v.get("sha256"),v.get("md5")],sort_keys=True))
`
const rInventory = `cat(R.version.string, "\n"); cat(R.version$platform, "\n"); cat("library-order:",paste(.libPaths(),collapse="|"),"\n"); ip <- installed.packages(noCache=TRUE); ip <- ip[order(ip[,"LibPath"],ip[,"Package"],ip[,"Version"]),c("Package","Version","LibPath","Built"),drop=FALSE]; write.table(ip,row.names=FALSE,col.names=FALSE,quote=TRUE,sep="\t")`
const juliaInventory = `using TOML, UUIDs
println((VERSION,Sys.MACHINE,DEPOT_PATH,LOAD_PATH))
project=Base.active_project()
manifest=joinpath(dirname(project),"Manifest.toml")
if isfile(manifest)
 data=TOML.parsefile(manifest)
 TOML.print(stdout,data;sorted=true)
 for (name,entries) in sort!(collect(get(data,"deps",Dict()));by=first)
  for entry in entries
   id=UUID(entry["uuid"])
   println(("installed",name,string(id),Base.locate_package(Base.PkgId(id,name))))
  end
 end
else
 println("no manifest")
end
`
