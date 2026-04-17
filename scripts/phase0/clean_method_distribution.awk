#!/usr/bin/awk -f

/pipeline: cleaned/ {
	method = ""
	for (i = 1; i <= NF; i++) {
		if ($i ~ /^method=/) {
			split($i, pair, "=")
			method = pair[2]
			gsub(/"/, "", method)
			if (method != "") {
				counts[method]++
				total++
			}
		}
	}
}

END {
	if (total == 0) {
		print "No pipeline cleaned method entries found in log."
		exit 1
	}

	for (m in counts) {
		printf "%s %d %.2f%%\n", m, counts[m], (counts[m] * 100) / total
	}
	printf "total %d\n", total
}
