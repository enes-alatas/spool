package main

// The docs site serves the latest release's docs at its root (ADR-0071),
// so a build's messages name the pages of the release an operator
// upgrades to.
const docsSite = "https://enes-alatas.github.io/spool/"

// The pages and sections the upgrade's messages send an operator to.
// docs_test.go holds each to a heading in site/content.
const (
	docsWithUpgrade   = docsSite + "run-a-hub/upgrade/#with-spool-upgrade"
	docsByHand        = docsSite + "run-a-hub/upgrade/#by-hand"
	docsRollingBack   = docsSite + "run-a-hub/upgrade/#rolling-back"
	docsBackupScripts = docsSite + "run-a-hub/backup/#installing-the-scripts"
)
