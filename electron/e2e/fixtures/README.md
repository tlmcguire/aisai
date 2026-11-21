# flask (fixture)

A vendored fixture package. It exists so that the end-to-end test can audit
something without touching the network. The code is deliberately small and
contains the two patterns the test looks for: a secret that is embedded in
the source and a comparison that is not constant time.
