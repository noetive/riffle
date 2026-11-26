# Colours Riffle's replies for the demo recording; Riffle itself prints plain text.
# Quoted page text is green, refs yellow, roles blue, other words magenta.
# Removed lines are red, added lines green, and a changed line's ~ yellow with
# its +gained and -lost words green and red. A failure is red.

BEGIN {
	esc = sprintf("%c", 27)
	reset = esc "[0m"
	green = esc "[32m"; yellow = esc "[33m"; blue = esc "[34m"
	magenta = esc "[35m"; red = esc "[31m"; redbold = esc "[1;31m"; dim = esc "[90m"
}

function paint(c, s) { return c s reset }
function isref(t) { return t ~ /^[a-z][0-9]+$/ }

# word colours one bare word outside quotes; role says it names what follows.
function word(t,    k) {
	if (failed) return paint(redbold, t)
	if (t == "|") { role = 1; return paint(dim, t) }
	if (marker && (t == "-" || t == "+" || t == "~")) {
		marker = 0
		return paint(t == "-" ? red : (t == "+" ? green : yellow), t)
	}
	marker = 0
	if (isref(t)) { role = 0; return paint(yellow, t) }
	if (role) { role = 0; return paint(blue, t) }
	# On a changed line, +word and -word are what was gained and lost.
	c = substr(t, 1, 1)
	if (changed && (c == "+" || c == "-")) return paint(c == "+" ? green : red, t)
	k = index(t, "=")
	if (k > 0) {
		rest = substr(t, k + 1)
		return paint(magenta, substr(t, 1, k)) (isref(rest) ? paint(yellow, rest) : rest)
	}
	return paint(magenta, t)
}

# A removed or added line reads as a diff: the whole line red or green.
/^- / { print paint(red, $0); next }
/^\+ / { print paint(green, $0); next }

{
	failed = ($0 ~ /^failed line [0-9]+:/)
	changed = ($0 ~ /^~ /)
	marker = 1; role = 1
	out = ""
	n = split($0, part, "\"")
	for (i = 1; i <= n; i++) {
		if (i % 2 == 0) { out = out paint(green, "\"" part[i] "\""); continue }
		m = split(part[i], w, " ")
		# split drops the spacing, so walk the segment to keep indentation.
		seg = part[i]; j = 1
		while (length(seg) > 0) {
			if (substr(seg, 1, 1) == " ") { out = out " "; seg = substr(seg, 2); continue }
			out = out word(w[j])
			seg = substr(seg, length(w[j]) + 1); j++
		}
	}
	print out
}
