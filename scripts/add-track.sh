#!/usr/bin/env bash
# Turn a media file into a signed spor track directory, and optionally publish it.
#
#   scripts/add-track.sh ~/Downloads/song.mp3
#   scripts/add-track.sh -s synthwave -v unlisted --publish ~/Downloads/song.flac
#   scripts/add-track.sh ~/Exports/song.opus ~/Exports/song.mp3   # several sources, one track
#
# Title, genre and the original creation date come from the file's tags
# (ffprobe), falling back to the file name. The publish date is today. The cover comes from -c, else an image next to the
# file (cover/folder/front/<name>.jpg|png), else the embedded artwork. WAV and
# AIFF are transcoded to opus + mp3 because browsers handle those best.
set -euo pipefail

usage() {
	cat <<'EOF'
usage: add-track.sh [options] MEDIA [MEDIA...]

  -t TITLE        title             (default: title tag, else file name)
  -S SLUG         directory / URL slug (default: from title)
  -d DATE         publish date, YYYY-MM-DD   (default: today)
  -C CREATED      originally made, YYYY, YYYY-MM or YYYY-MM-DD
                                    (default: date or creation_time tag)
  -s STYLE        style / genre     (default: genre tag)
  -D TEXT         description       (default: description or comment tag)
  -v VISIBILITY   public | unlisted | draft   (default: public)
  -c IMAGE        cover image
  -l FILE         lyrics text file
  -L DIR          library directory (default: $SPOR_LIBRARY, else ./library)
  -f              replace an existing track directory
  -p, --publish   sign, rsync to $SPOR_REMOTE and reload (spor publish)
  -h              this help

Environment: SPOR_BIN (spor binary), SPOR_KEY, SPOR_URL, SPOR_REMOTE, SPOR_LIBRARY.
EOF
}

die() { echo "add-track: $*" >&2; exit 1; }

title='' slug='' date='' created='' style='' desc='' vis=public cover='' lyrics='' force='' publish=''
lib=${SPOR_LIBRARY:-library}
args=()
while (($#)); do
	case $1 in
		-t) title=$2; shift ;;
		-S) slug=$2; shift ;;
		-d) date=$2; shift ;;
		-C) created=$2; shift ;;
		-s) style=$2; shift ;;
		-D) desc=$2; shift ;;
		-v) vis=$2; shift ;;
		-c) cover=$2; shift ;;
		-l) lyrics=$2; shift ;;
		-L) lib=$2; shift ;;
		-f) force=1 ;;
		-p | --publish) publish=1 ;;
		-h | --help) usage; exit 0 ;;
		--) shift; args+=("$@"); break ;;
		-*) die "unknown option $1 (see -h)" ;;
		*) args+=("$1") ;;
	esac
	shift
done
((${#args[@]})) || { usage >&2; exit 2; }
case $vis in public | unlisted | draft) ;; *) die "visibility must be public, unlisted or draft" ;; esac
[[ -z $publish || -n ${SPOR_REMOTE:-} ]] || die "set SPOR_REMOTE (e.g. root@vps:/srv/spor/library) to publish"
[[ -z $date || $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "date must be YYYY-MM-DD"
created_re='^[0-9]{4}(-[0-9]{2}(-[0-9]{2})?)?$'
[[ -z $created || $created =~ $created_re ]] || die "created must be YYYY, YYYY-MM or YYYY-MM-DD"
for f in "${args[@]}" ${cover:+"$cover"} ${lyrics:+"$lyrics"}; do
	[[ -f $f ]] || die "no such file: $f"
done
command -v ffprobe >/dev/null || die "ffprobe is required"
command -v ffmpeg >/dev/null || die "ffmpeg is required"

# Find spor: $SPOR_BIN, then PATH, then build from this checkout.
repo=$(cd "$(dirname "$0")/.." && pwd)
spor=${SPOR_BIN:-}
if [[ -z $spor ]]; then
	if command -v spor >/dev/null; then
		spor=$(command -v spor)
	else
		spor=$repo/spor
		if [[ ! -x $spor || -n $(find "$repo" -name '*.go' -newer "$spor" -print -quit) ]]; then
			echo "building $spor"
			(cd "$repo" && go build -o spor .) || die "go build failed (Go >= 1.25 needed)"
		fi
	fi
fi

first=${args[0]}

# tag NAME: first matching tag of the first file, format or stream level, any case.
# File-level tags first, then audio streams (Ogg/Opus keep tags there). Video
# streams are skipped: embedded cover art carries tags like comment="Other".
probe=$(ffprobe -v error -show_entries format_tags -of flat "$first" 2>/dev/null || true)
probe+=$'\n'$(ffprobe -v error -select_streams a -show_entries stream_tags -of flat "$first" 2>/dev/null || true)
tag() {
	grep -iE "^(format|streams\.stream\.[0-9]+)\.tags\.$1=" <<<"$probe" | head -n1 |
		sed -E 's/^[^=]*="(.*)"$/\1/; s/\\"/"/g; s/\\\\/\\/g' || true
}

if [[ -z $title ]]; then
	title=$(tag title)
	[[ -n $title ]] || { title=$(basename "${first%.*}"); title=${title//_/ }; }
fi
[[ -n $date ]] || date=$(date +%F)
if [[ -z $created ]]; then
	for t in date creation_time; do
		c=$(tag "$t" | cut -c1-10)
		# Some taggers write 2019/03/12 or 2019.03.12.
		c=${c//\//-}
		c=${c//./-}
		for cand in "$c" "${c:0:7}" "${c:0:4}"; do
			[[ $cand =~ $created_re ]] && { created=$cand; break 2; }
		done
	done
fi
[[ -n $style ]] || style=$(tag genre)
[[ -n $desc ]] || desc=$(tag description)
[[ -n $desc ]] || desc=$(tag comment)

if [[ -z $slug ]]; then
	slug=$(printf '%s' "$title" | tr '[:upper:]' '[:lower:]' |
		sed 's/æ/ae/g; s/ø/o/g; s/å/a/g; s/ä/a/g; s/ö/o/g; s/ü/u/g; s/é/e/g; s/è/e/g' |
		iconv -f utf-8 -t ascii//TRANSLIT 2>/dev/null |
		sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' | cut -c1-80)
	slug=${slug%-}
	[[ -n $slug ]] || slug=track-$(date +%Y%m%d%H%M%S)
fi
[[ $slug =~ ^[a-z0-9_][a-z0-9_.-]*$ ]] || die "invalid slug '$slug' (a-z, 0-9, - _ .)"

dir=$lib/$slug
if [[ -e $dir ]]; then
	[[ -n $force ]] || die "$dir already exists (use -f to replace it, or -S for another slug)"
	rm -rf -- "$dir"
fi
mkdir -p "$dir"
trap '[[ -n ${ok:-} ]] || rm -rf -- "$dir"' EXIT

# Media files.
have_video=
for f in "${args[@]}"; do
	ext=$(tr '[:upper:]' '[:lower:]' <<<"${f##*.}")
	case $ext in
		opus | ogg | oga | webm | m4a | aac | flac | mp3) cp -- "$f" "$dir/track.$ext" ;;
		mp4 | m4v) cp -- "$f" "$dir/track.$ext"; have_video=1 ;;
		wav | aif | aiff)
			echo "transcoding $(basename "$f") to opus + mp3"
			ffmpeg -nostdin -loglevel error -i "$f" -map 0:a:0 -c:a libopus -b:a 160k "$dir/track.opus"
			ffmpeg -nostdin -loglevel error -i "$f" -map 0:a:0 -c:a libmp3lame -q:a 2 "$dir/track.mp3"
			;;
		*) die "unsupported media type .$ext ($f)" ;;
	esac
done

# Cover: explicit, then a sibling image, then embedded artwork.
if [[ -z $cover ]]; then
	src=$(dirname "$first") base=$(basename "${first%.*}")
	for name in "$base" cover folder front Cover Folder Front; do
		for e in jpg jpeg png JPG JPEG PNG; do
			[[ -f $src/$name.$e ]] && { cover=$src/$name.$e; break 2; }
		done
	done
fi
if [[ -n $cover ]]; then
	ext=$(tr '[:upper:]' '[:lower:]' <<<"${cover##*.}")
	case $ext in
		jpg | jpeg | png) cp -- "$cover" "$dir/cover.$ext" ;;
		*) ffmpeg -nostdin -loglevel error -i "$cover" -frames:v 1 "$dir/cover.jpg" ;;
	esac
elif [[ -z $have_video ]]; then
	ffmpeg -nostdin -loglevel error -i "$first" -an -map 0:v:0 -frames:v 1 "$dir/cover.jpg" 2>/dev/null ||
		{ rm -f "$dir/cover.jpg"; echo "note: no cover found"; }
fi

[[ -n $lyrics ]] && cp -- "$lyrics" "$dir/lyrics.txt"

# meta.toml, quoted the same way as conf.Quote.
q() {
	local s=${1//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$'\n'/\\n}
	s=${s//$'\t'/\\t}
	printf '"%s"' "${s//$'\r'/\\r}"
}
{
	echo "title = $(q "$title")"
	echo "date = $(q "$date")"
	[[ -n $created ]] && echo "created = $(q "$created")"
	[[ -n $style ]] && echo "style = $(q "$style")"
	[[ -n $desc ]] && echo "description = $(q "$desc")"
	echo "visibility = $(q "$vis")"
} >"$dir/meta.toml"

[[ -n $style ]] || echo "warning: no style/genre (set it with -s, or a genre tag)" >&2
[[ -n $desc ]] || echo "warning: no description (set it with -D, or a comment tag)" >&2
echo "--- $dir/meta.toml"
cat "$dir/meta.toml"
echo "---"

# The directory is complete; keep it even if signing fails so it can be retried.
ok=1
if [[ -n $publish ]]; then
	"$spor" publish "$dir" || die "publish failed; retry with: $spor publish $dir"
else
	"$spor" sign "$dir" || die "sign failed; retry with: $spor sign $dir"
fi
