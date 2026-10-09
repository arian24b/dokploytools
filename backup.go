package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func pickDest(p Panel, want string) (obj, error) {
	var v any
	if err := p.call("GET", "destination.all", nil, &v); err != nil {
		return nil, err
	}
	ds := list(v)
	if len(ds) == 0 {
		return nil, fmt.Errorf("no backup destination configured in panel")
	}
	for _, d := range ds {
		if want != "" && (str(d["destinationId"]) == want || str(d["name"]) == want) {
			return d, nil
		}
	}
	if want != "" {
		return nil, fmt.Errorf("destination %q not found", want)
	}
	return ds[0], nil // ponytail: first destination when --dest omitted
}

func s3client(ep, access, secret, region string) (*minio.Client, error) {
	secure := !strings.HasPrefix(ep, "http://")
	ep = strings.TrimPrefix(strings.TrimPrefix(ep, "https://"), "http://")
	return minio.New(ep, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: secure, Region: region})
}

func destClient(d obj) (*minio.Client, error) {
	return s3client(str(d["endpoint"]), str(d["accessKey"]), str(d["secretAccessKey"]), str(d["region"]))
}

// newest returns the latest object under prefix; with wait it polls up to ~2min for one modified after `after`.
func newest(mc *minio.Client, bucket, prefix string, after time.Time, wait bool) (minio.ObjectInfo, error) {
	var best minio.ObjectInfo
	for i := 0; i < 24; i++ {
		best = minio.ObjectInfo{}
		for o := range mc.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if o.Err == nil && o.LastModified.After(best.LastModified) {
				best = o
			}
		}
		if !wait || best.LastModified.After(after) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if best.Key == "" {
		return best, fmt.Errorf("no backup file found under %q", prefix)
	}
	if wait && !best.LastModified.After(after) {
		return best, fmt.Errorf("backup file did not appear in time (latest is %s)", best.LastModified.Format(time.RFC3339))
	}
	return best, nil
}

// mirror copies one object to the --to-* S3 target under <to-prefix>/<panel>/<file>.
func mirror(src *minio.Client, bucket string, o minio.ObjectInfo, panel string, f F) error {
	env := func(flag, e string) string { return f.or(flag, os.Getenv(e)) }
	ep, tb := env("to-endpoint", "DOKPLOYTOOLS_TO_ENDPOINT"), env("to-bucket", "DOKPLOYTOOLS_TO_BUCKET")
	dst, err := s3client(ep, env("to-access", "DOKPLOYTOOLS_TO_ACCESS"), env("to-secret", "DOKPLOYTOOLS_TO_SECRET"), f.s("to-region"))
	if err != nil {
		return err
	}
	obj, err := src.GetObject(context.Background(), bucket, o.Key, minio.GetObjectOptions{})
	if err != nil {
		return err
	}
	defer obj.Close()
	key := strings.Trim(f.s("to-prefix")+"/"+panel+"/"+filepath.Base(o.Key), "/")
	_, err = dst.PutObject(context.Background(), tb, key, obj, o.Size, minio.PutObjectOptions{})
	if err == nil {
		done("copied to %s/%s", tb, key)
	}
	return err
}

// afterBackup handles --download DIR and --to-* mirroring of the freshly created file.
func afterBackup(p Panel, d obj, prefix string, started time.Time, f F) error {
	dir := f.s("download")
	if dir == "" && !f.has("to-endpoint") && os.Getenv("DOKPLOYTOOLS_TO_ENDPOINT") == "" {
		return nil
	}
	mc, err := destClient(d)
	if err != nil {
		return err
	}
	bucket := str(d["bucket"])
	o, err := newest(mc, bucket, prefix, started.Add(-time.Minute), true)
	if err != nil {
		return err
	}
	if dir != "" {
		os.MkdirAll(dir, 0o755)
		dst := filepath.Join(dir, p.Name+"-"+filepath.Base(o.Key))
		if err := mc.FGetObject(context.Background(), bucket, o.Key, dst, minio.GetObjectOptions{}); err != nil {
			return err
		}
		done("downloaded %s", dst)
	}
	if f.has("to-endpoint") || os.Getenv("DOKPLOYTOOLS_TO_ENDPOINT") != "" {
		return mirror(mc, bucket, o, p.Name, f)
	}
	return nil
}

// cmdBackup: Dokploy web-server (panel itself) backup.
func cmdBackup(p Panel, f F) error {
	d, err := pickDest(p, f.s("dest"))
	if err != nil {
		return err
	}
	find := func() obj {
		var u obj
		if p.call("GET", "user.getBackups", nil, &u) != nil {
			return nil
		}
		for _, b := range list(u["backups"]) {
			if str(b["databaseType"]) == "web-server" && str(b["destinationId"]) == str(d["destinationId"]) {
				return b
			}
		}
		return nil
	}
	b := find()
	if b == nil {
		err := p.call("POST", "backup.create", obj{"schedule": "0 0 * * *", "enabled": false, "prefix": "dokploy-web-server",
			"destinationId": d["destinationId"], "keepLatestCount": f.n("keep", 5), "database": "dokploy", "databaseType": "web-server"}, nil)
		if err != nil {
			return err
		}
		if b = find(); b == nil {
			return fmt.Errorf("created backup config but cannot find it")
		}
	}
	start := time.Now()
	if err := p.call("POST", "backup.manualBackupWebServer", obj{"backupId": b["backupId"]}, nil); err != nil {
		return err
	}
	done("web-server backup triggered to %s", str(d["name"]))
	return afterBackup(p, d, str(b["prefix"]), start, f)
}

// ---- database backups ----

var manualEP = map[string]string{"postgres": "backup.manualBackupPostgres", "mysql": "backup.manualBackupMySql",
	"mariadb": "backup.manualBackupMariadb", "mongo": "backup.manualBackupMongo"}

func backupsOf(p Panel, s svc) ([]obj, obj, error) {
	o, err := one(p, s)
	return list(o["backups"]), o, err
}

func resolveDB(p Panel, ref string, f F) (svc, error) {
	s, err := resolve(p, ref, f.s("env"))
	if err == nil && manualEP[s.Kind] == "" {
		err = fmt.Errorf("%s has kind %q; db backups support postgres/mysql/mariadb/mongo", ref, s.Kind)
	}
	return s, err
}

// cmdDBBackup: dbbackup list|create|run|set|rm|files|download|copy
func cmdDBBackup(p Panel, args []string, f F) error {
	arg := func(i int) string {
		if len(args) > i {
			return args[i]
		}
		return ""
	}
	switch arg(0) {
	case "list":
		all, err := services(p)
		if err != nil {
			return err
		}
		rows := [][]string{{"SERVICE", "BACKUP", "SCHEDULE", "KEEP", "ENABLED", "DATABASE", "PREFIX", "DEST"}}
		for _, s := range all {
			if manualEP[s.Kind] == "" || (arg(1) != "" && s.Project+"/"+s.Name != arg(1)) {
				continue
			}
			bs, _, err := backupsOf(p, s)
			if err != nil {
				return err
			}
			for _, b := range bs {
				rows = append(rows, []string{s.Project + "/" + s.Name, str(b["backupId"]), str(b["schedule"]), str(b["keepLatestCount"]),
					str(b["enabled"]), str(b["database"]), str(b["prefix"]), str(b["destinationId"])})
			}
		}
		table(rows)
	case "create":
		s, err := resolveDB(p, arg(1), f)
		if err != nil {
			return err
		}
		d, err := pickDest(p, f.s("dest"))
		if err != nil {
			return err
		}
		_, o, err := backupsOf(p, s)
		if err != nil {
			return err
		}
		dbName := f.or("database", str(o["databaseName"]))
		body := obj{"schedule": f.or("schedule", "0 2 * * *"), "enabled": !f.has("disabled"), "prefix": f.or("prefix", s.Name),
			"destinationId": d["destinationId"], "keepLatestCount": f.n("keep", 7), "database": dbName,
			"databaseType": s.Kind, s.Kind + "Id": s.ID}
		if err := p.call("POST", "backup.create", body, nil); err != nil {
			return err
		}
		done("backup created for %s/%s (%s, keep %v)", s.Project, s.Name, body["schedule"], body["keepLatestCount"])
	case "run":
		s, err := resolveDB(p, arg(1), f)
		if err != nil {
			return err
		}
		bs, _, err := backupsOf(p, s)
		if err != nil {
			return err
		}
		var b obj
		for _, x := range bs {
			if f.s("id") == "" || str(x["backupId"]) == f.s("id") {
				if b != nil {
					return fmt.Errorf("several backups configured; pass --id")
				}
				b = x
			}
		}
		if b == nil {
			return fmt.Errorf("no backup configured (use dbbackup create)")
		}
		start := time.Now()
		if err := p.call("POST", manualEP[s.Kind], obj{"backupId": b["backupId"]}, nil); err != nil {
			return err
		}
		done("backup triggered for %s/%s", s.Project, s.Name)
		var d obj
		if d, err = pickDest(p, str(b["destinationId"])); err != nil {
			return err
		}
		return afterBackup(p, d, str(b["prefix"]), start, f)
	case "set": // dbbackup set PANEL BACKUPID [--schedule --keep --enable true|false --dest]
		var b obj
		if err := p.call("GET", "backup.one", obj{"backupId": arg(1)}, &b); err != nil {
			return err
		}
		body := obj{"backupId": arg(1), "schedule": f.or("schedule", str(b["schedule"])), "prefix": f.or("prefix", str(b["prefix"])),
			"destinationId": f.or("dest", str(b["destinationId"])), "database": str(b["database"]), "databaseType": b["databaseType"],
			"enabled": b["enabled"], "keepLatestCount": b["keepLatestCount"], "serviceName": b["serviceName"], "metadata": b["metadata"]}
		if f.has("keep") {
			body["keepLatestCount"] = f.n("keep", 7)
		}
		if f.has("enable") {
			body["enabled"] = f.s("enable") != "false"
		}
		if err := p.call("POST", "backup.update", body, nil); err != nil {
			return err
		}
		done("backup %s updated", arg(1))
	case "rm":
		if err := p.call("POST", "backup.remove", obj{"backupId": arg(1)}, nil); err != nil {
			return err
		}
		done("backup %s removed", arg(1))
	case "files":
		d, err := pickDest(p, f.s("dest"))
		if err != nil {
			return err
		}
		var v any
		if err := p.call("GET", "backup.listBackupFiles", obj{"destinationId": d["destinationId"], "search": f.s("search")}, &v); err != nil {
			return err
		}
		rows := [][]string{{"FILE"}}
		if a, ok := v.([]any); ok {
			for _, x := range a {
				rows = append(rows, []string{compact(x)})
			}
		} else {
			rows = append(rows, []string{compact(v)})
		}
		table(rows)
	case "download", "copy": // download: dbbackup download PANEL --dest D --search PREFIX --download DIR ; copy: --to-*
		d, err := pickDest(p, f.s("dest"))
		if err != nil {
			return err
		}
		if arg(0) == "download" && f.s("download") == "" {
			return fmt.Errorf("pass --download DIR")
		}
		mc, err := destClient(d)
		if err != nil {
			return err
		}
		o, err := newest(mc, str(d["bucket"]), f.s("search"), time.Time{}, false)
		if err != nil {
			return err
		}
		if dir := f.s("download"); dir != "" {
			os.MkdirAll(dir, 0o755)
			dst := filepath.Join(dir, p.Name+"-"+filepath.Base(o.Key))
			if err := mc.FGetObject(context.Background(), str(d["bucket"]), o.Key, dst, minio.GetObjectOptions{}); err != nil {
				return err
			}
			done("downloaded %s", dst)
		}
		if arg(0) == "copy" {
			return mirror(mc, str(d["bucket"]), o, p.Name, f)
		}
	default:
		return fmt.Errorf("dbbackup list|create|run|set|rm|files|download|copy (restore is not exposed by the Dokploy REST API)")
	}
	return nil
}
