# Renders the README animation of a Riffle session against the shop page beside it.
# Run from the repository root: vhs .github/demo/riffle.tape
# Needs vhs, Go, python3, Chrome and bash 4.4 or later. It builds Riffle from this checkout.

Output .github/demo/riffle.gif

Require go
Require python3

Set Shell bash
Set FontSize 20
Set Width 1200
Set Height 760
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms

Hide
# Riffle from this checkout and a daemon on a socket of its own, so the demo
# leaves your usual Riffle and its policy alone. What is typed shows in bold
# white; Riffle's replies are coloured by highlight.awk.
Type `D=$(mktemp -d); go build -o $D/riffle ./cmd/riffle && export PATH=$D:$PATH; PS1='\[\e[35m\]$ \[\e[0;1;97m\]'; PS2='\[\e[35m\]> \[\e[0;1;97m\]'; PS0='\e[0m'; riffle() { local c=$1; shift; command riffle $c -socket $D/riffle.sock "$@" 2>&1 | awk -f .github/demo/highlight.awk; }; python3 -m http.server 8765 --bind 127.0.0.1 -d .github/demo >/dev/null 2>&1 & SRV=$!; riffle serve -allow-private >/dev/null 2>&1 & sleep 2; clear`
Enter
Sleep 15s
Show

Type "# Open a shop page and see it as text"
Enter
Type "echo 'goto http://shop.localhost:8765/' | riffle run"
Enter
Sleep 5s

Type "# The cart is behind a cookie dialog, so a click is refused"
Enter
Type `echo 'click "Checkout"' | riffle run`
Enter
Sleep 4s

Type "# Close the dialog; the reply is only what changed"
Enter
Type `echo 'click "Reject"' | riffle run`
Enter
Sleep 5s

Type "clear"
Enter
Type "# Several steps in one call, then the diff"
Enter
Type "riffle run <<'EOF'"
Enter
Type 'fill "Qty" "2"'
Enter
Type 'click "Checkout"'
Enter
Type 'expect text "Order placed"'
Enter
Type "EOF"
Enter
Sleep 6s

Hide
Type "riffle stop >/dev/null 2>&1; kill $SRV; rm -rf $D"
Enter
Sleep 3s
