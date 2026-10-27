#!/bin/sh
set -eu
cd "$(dirname "$0")/.."

run_root() {
    if [ "$(id -u)" = 0 ]; then "$@"
    elif command -v doas >/dev/null 2>&1; then doas "$@"
    elif command -v sudo >/dev/null 2>&1; then sudo "$@"
    else echo "Install requires root, doas or sudo." >&2; exit 1
    fi
}

user=${TFS_WEB_USER:-$(id -un)}
case "$user" in ''|*[!a-zA-Z0-9_-]*) echo "Invalid service user." >&2; exit 1;; esac
if [ "$(id -u "$user")" = 0 ]; then
    echo "Set TFS_WEB_USER to an existing unprivileged account." >&2
    exit 1
fi
group=$(id -gn "$user")
case "$group" in ''|*[!a-zA-Z0-9_-]*) echo "Invalid service group." >&2; exit 1;; esac

mkdir -p bin
go build -trimpath -o bin/tfs-web .
# Keep installation atomic so rebuilding never overwrites a running executable.
run_root install -d /usr/local/libexec /etc/init.d /etc/conf.d
run_root install -m 0755 bin/tfs-web /usr/local/libexec/tfs-web.next
run_root mv /usr/local/libexec/tfs-web.next /usr/local/libexec/tfs-web
run_root install -m 0755 deploy/tfs-web.openrc /etc/init.d/tfs-web
# Preserve an existing administrator-managed service configuration.
if ! run_root test -e /etc/conf.d/tfs-web; then
    printf 'TFS_WEB_USER="%s:%s"\n' "$user" "$group" | run_root tee /etc/conf.d/tfs-web >/dev/null
    run_root chmod 0644 /etc/conf.d/tfs-web
fi
echo "Installed. Service was not started; autostart was not enabled."
