#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  printf 'Usage: %s DATA_DIR BACKUP_ARCHIVE\n' "$0" >&2
  exit 2
fi

if [ ! -d "$1" ]; then
  printf 'Data directory does not exist: %s\n' "$1" >&2
  exit 1
fi

data_dir=$(cd "$1" && pwd -P)
backup_name=$(basename "$2")
if [ "$backup_name" = "." ] || [ "$backup_name" = ".." ]; then
  printf 'Backup archive must name a file\n' >&2
  exit 1
fi
backup_parent=$(dirname "$2")
mkdir -p "$backup_parent"
backup_parent=$(cd "$backup_parent" && pwd -P)
backup_path=$backup_parent/$backup_name

case "$backup_path/" in
  "$data_dir/"*)
    printf 'Backup archive must be outside the data directory\n' >&2
    exit 1
    ;;
esac

if [ -d "$backup_path" ]; then
    printf 'Backup destination must not be a directory: %s\n' "$backup_path" >&2
    exit 1
fi

if [ -L "$backup_path" ]; then
  printf 'Refusing to replace a symbolic link: %s\n' "$backup_path" >&2
  exit 1
fi

temporary_archive=$(mktemp "$backup_parent/.syncforge-backup.XXXXXX")
trap 'rm -f "$temporary_archive"' EXIT HUP INT TERM

tar -czf "$temporary_archive" -C "$data_dir" .
chmod 600 "$temporary_archive"
mv -f "$temporary_archive" "$backup_path"
trap - EXIT HUP INT TERM
printf 'Backup created: %s\n' "$backup_path"
