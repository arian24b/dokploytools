# dokploytools

[English](README.md)

ابزار خط فرمان (Go) برای مدیریت چند پنل [Dokploy](https://dokploy.com) از یک‌جا. `NAME` نام پنل است؛ اگر آن را ننویسید (یا `all` بنویسید) دستور روی همهٔ پنل‌ها اجرا می‌شود.

```
go build -o dokploytools ./src
ln -sf "$PWD/dokploytools" ~/.local/bin/dpt   # نام کوتاه: dpt
```

فلگ‌های عمومی: `--json` (خروجی ماشینی؛ اگر پنلی خطا بدهد کد خروج ۱ است) و `--watch SECS` (تکرار دستور).

## پنل‌ها و تنظیمات
```
panel add NAME URL (--key KEY | --user EMAIL --pass PASS)
panel list | panel edit NAME [--url U] [--key K] | panel rm NAME | panel encrypt
```
- تنظیمات در `~/.config/dokploytools/panels.json` (دسترسی 0600) ذخیره می‌شود. اگر `DOKPLOYTOOLS_PASSPHRASE` تنظیم شود، کلیدهای API با AES-GCM رمزنگاری می‌شوند؛ `panel encrypt` فایل موجود را رمز می‌کند.
- بدون فایل هم می‌شود: `DOKPLOY_<NAME>_URL` و `DOKPLOY_<NAME>_KEY` یک پنل با نام `<name>` تعریف می‌کنند (روی دیسک ذخیره نمی‌شود).
- با `--user/--pass` ابزار یک بار وارد می‌شود، کلید API می‌سازد و فقط همان را نگه می‌دارد.

## نمای کلی
```
projects | status [--project P] | servers [--check] | containers [--stats]
deployments NAME PROJ/SVC | monitor | update [--check]
alert [--cpu 90 --mem 90 --disk 90 --cert-days 14 --no-certs --webhook URL --telegram TOKEN@CHATID]
```
`alert` وقتی پنل از دسترس خارج باشد، سرویس/سرور در وضعیت خطا باشد، مصرف از حد بگذرد یا گواهی یک دامنهٔ HTTPS نزدیک انقضا باشد با کد ۱ خارج می‌شود؛ برای cron مناسب است.

## سرویس‌ها
```
svc start|stop|deploy|redeploy NAME PROJ/SVC [--env E] [--wait]     # NAME=all روی همهٔ پنل‌ها
env NAME PROJ/SVC [set K=V.. | unset K.. | --file F]
```
**دیپلوی docker-compose از گیت با یک دستور** (پروژه/environment/compose را در صورت نبود می‌سازد، در غیر این صورت به‌روز می‌کند):
```
dokploytools deploy prod --repo https://github.com/me/app.git --branch main \
  --domain app.example.com --service web --port 3000 --set KEY=val --wait
```
فلگ‌های دیگر: `--project P` (پیش‌فرض: نام ریپو)، `--env production`، `--name`، `--compose-path`، `--env-file F`، `--ssh-key-id`، `--server ID`، `--auto-deploy`، `--no-deploy`، `--no-https`. با `--file compose.yml` به‌جای ریپو، فایل محلی دیپلوی می‌شود.

## بکاپ
```
backup [NAME] [--dest D] [--keep N] [--download DIR]                        # بکاپ خود پنل (web-server)
dbbackup create NAME PROJ/SVC [--dest D --schedule "0 2 * * *" --keep 7 --database DB --prefix P --disabled]
dbbackup list NAME [PROJ/SVC] | run NAME PROJ/SVC [--id B] | set NAME BACKUPID [--schedule --keep --enable false] | rm NAME BACKUPID
dbbackup files NAME --dest D --search PREFIX | download NAME --dest D --search PREFIX --download DIR
dbbackup copy all --dest D --search PREFIX --to-endpoint E --to-bucket B --to-access K --to-secret S [--to-prefix P]
```
دستورهای `backup` و `dbbackup run` فلگ `--download DIR` و فلگ‌های `--to-*` (یا `DOKPLOYTOOLS_TO_ENDPOINT/BUCKET/ACCESS/SECRET`) را هم می‌پذیرند تا فایل جدید در یک باکت مرکزی به‌صورت `<to-prefix>/<panel>/` کپی شود. Postgres، MySQL، MariaDB و Mongo پشتیبانی می‌شوند. **ریستور** در REST API ی Dokploy وجود ندارد (از websocket استفاده می‌کند) و پیاده‌سازی نشده است.

## دامنه، گواهی، Traefik
```
domains | domain add NAME PROJ/SVC --host H --port N [--service S] [--cert letsencrypt|none|custom] [--no-https]
domain rm NAME HOST | domain check NAME [HOST]        # بررسی DNS توسط پنل + انقضای TLS زنده
cert list NAME | cert add NAME CERTNAME --cert fullchain.pem --key privkey.pem [--domain H] [--auto-renew] | cert rm NAME ID|NAME
traefik get NAME main|web|middleware | traefik get NAME --path FILE | traefik ls NAME
traefik set NAME main|web|middleware --from FILE | traefik reload NAME
```
برای دامنه‌ای که DNS آن **روی همین سرور نیست** (پشت CDN یا سرور دیگر و چالش HTTP لت‌س‌انکریپت کار نمی‌کند): گواهی را با `cert add` آپلود کنید (جفت کلید، انقضا و پوشش `--domain` به‌صورت محلی بررسی می‌شود) و دامنه را با `--cert none` اضافه کنید؛ Traefik گواهی را بر اساس SNI سرو می‌کند. `traefik set` پیش از نوشتن، پیکربندی قبلی را در `traefik-<panel>-<file>-<ts>.bak` ذخیره می‌کند.

## کاربران
```
users | member invite NAME EMAIL [--role admin] | member rm NAME USERID | member invitations NAME | member uninvite NAME ID
apikey list|create NAME [--days N]|rm NAME ID | orgs
```

## CI و انتشار
- `.github/workflows/ci.yml`: بررسی gofmt، `go vet` و `go test -race` روی هر push به `main` و هر PR.
- `.github/workflows/release.yml`: با push یک تگ (`git tag v1.2.3 && git push --tags`) تست‌ها اجرا می‌شود، باینری‌های linux/darwin/windows ساخته می‌شوند (نسخه داخل باینری است: `dokploytools version`) و یک GitHub Release با checksum منتشر می‌شود.

## وضعیت
مسیرها و بدنهٔ درخواست‌ها طبق مستندات Dokploy است و روی پنل واقعی تست نشده. نامطمئن‌ترین بخش‌ها: ساختار `project.all`، پورت مانیتورینگ و `/metrics`، `user.getContainerMetrics`، مسیر دعوت عضو و ورود با رمز عبور. برای بررسی از `raw NAME GET PATH` استفاده کنید. دستور `go test ./...` یک تست با سرور ساختگی اجرا می‌کند.
