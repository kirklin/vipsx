#!/bin/sh
# Downloads the large NASA fixtures that the small synthetic images cannot
# stand in for.
#
# Nothing here enters the repository. The imagery is public domain, but four
# gigabytes of it in the module zip would be charged to everyone running
# `go get` on this repository, for the same reason site/ carries its own
# go.mod. It lands outside the checkout and the tests find it through
# VIPSX_IMAGE_DIR.
#
# Two formats, deliberately. Both sets are 21600x21600 RGB tiles of the same
# planet, so the pixel counts match and only the container differs: PNG has no
# random access and forces a whole-image decode, while the GeoTIFFs can be read
# a region at a time. A loader bug that only shows up when the whole thing must
# be held at once needs the first; the tiled read path needs the second.
#
# Usage: internal/soak/fetch-bigdata.sh [dir]
set -eu

dir=${1:-${XDG_CACHE_HOME:-$HOME/.cache}/vipsx-images}
mkdir -p "$dir"

bluemarble=https://eoimages.gsfc.nasa.gov/images/imagerecords/73000/73909
blackmarble=https://eoimages.gsfc.nasa.gov/images/imagerecords/144000/144898

# A2 is left out of both sets. It is the tile that is almost entirely ocean, so
# it compresses to well under half the size of its neighbours and would not
# exercise anything the others do not.
tiles="A1 B1 B2 C1 C2 D1 D2"

# High because a retry that is not needed costs nothing, while a link that
# drops every few megabytes needs one attempt per few megabytes to get through
# a three hundred megabyte file. The cap exists to stop a genuinely dead URL
# from looping forever, not to ration retries.
attempts=${FETCH_ATTEMPTS:-300}

# Bytes on disk, or 0 for a file that is not there yet. wc rather than stat,
# whose size flag is spelled differently on BSD and GNU.
have() {
	if [ -f "$1" ]; then wc -c <"$1" | tr -d ' '; else echo 0; fi
}

# Downloads until the file on disk matches the length the server advertises.
#
# A single curl is not enough. These transfers are hundreds of megabytes and
# run for minutes, and anything in the path that gets bored -- a corporate
# proxy, a local one, a flaky link -- closes the connection partway and curl
# exits 18 with most of the file missing. Both hosts advertise Accept-Ranges:
# bytes, so each retry resumes where the last stopped and no byte is fetched
# twice. Completion is decided by comparing sizes, not by curl's exit status,
# because a resume of an already-complete file is itself an error (416).
fetch() {
	url=$1
	out=$dir/$(basename "$url")
	want=$(curl -fsIL "$url" | tr -d '\r' |
		awk 'tolower($1) == "content-length:" { v = $2 } END { print v }')
	if [ -z "$want" ]; then
		echo "cannot determine the size of $url" >&2
		return 1
	fi

	printf '%s  %s bytes\n' "$(basename "$url")" "$want"
	n=0
	while [ "$(have "$out")" -lt "$want" ]; do
		n=$((n + 1))
		if [ "$n" -gt "$attempts" ]; then
			echo "gave up on $url after $attempts attempts" >&2
			return 1
		fi
		[ "$n" -eq 1 ] || printf '  resuming at %s bytes (attempt %s)\n' \
			"$(have "$out")" "$n"
		# --speed-limit gives up on a connection that has stalled rather than
		# closed, which otherwise hangs until the socket times out.
		curl -fL --progress-bar -C - --speed-limit 2048 --speed-time 30 \
			-o "$out" "$url" || true
	done
}

for t in $tiles; do
	fetch "$bluemarble/world.topo.bathy.200412.3x21600x21600.$t.png"
	fetch "$blackmarble/BlackMarble_2016_${t}_geo.tif"
done

printf '\n'
ls -lh "$dir"
printf '\nexport VIPSX_IMAGE_DIR=%s\n' "$dir"
