package cloud

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"time"
)

// The CLI doesn't refuse an environment ID the account doesn't hold: it
// starts the session in the account's first environment, exits 0, and
// says so only in its debug log, in a line naming the environment it
// picked (run U on #1078). A start and a setup check ask for that log and
// read it as the CLI writes it, so a pick that isn't the loop's
// environment ends the run, usually before the session is created
// (#1232).
//
// The log also carries the wake's text, which whoever messages the loop
// writes, so the watch reads only lines in the CLI's own shape: a time
// and a level, then its words. In run U every line has that shape, and
// the wake's text comes after the pick, JSON-escaped inside the line that
// creates the session. That line, or one of any other shape, ends the
// watch, so the text can't forge a pick even written over many lines. A
// CLI that wrote the text over many lines before its pick could have a
// forged pick line read, and hold the loop (THREAT-MODEL).
var (
	cliLogLine   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[0-9:.]+Z \[[A-Z]+\] `)
	selectedLine = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[0-9:.]+Z \[DEBUG\] Selected environment: (env_[A-Za-z0-9]{1,100}) \(`)
	createLine   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[0-9:.]+Z \[DEBUG\] "?Creating session`)
)

const (
	// watchEvery is how often the log is read again: the CLI logs its pick
	// about a second before it creates the session.
	watchEvery = 50 * time.Millisecond
	// watchedBytes bounds how much of the log is read, past which the
	// watch gives up: the pick comes early, in the first few kilobytes.
	watchedBytes = 4 << 20
)

// watchEnvironment reads the CLI's debug log at path until done closes,
// and sends the environment the CLI picked when it isn't want. A pick of
// want, a line not in the CLI's shape, the line creating the session, a
// log past watchedBytes, or done ends the watch without a word.
func watchEnvironment(path, want string, done <-chan struct{}) <-chan string {
	picked := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(watchEvery)
		defer ticker.Stop()
		var read int64
		var partial []byte
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			chunk, err := readFrom(path, read)
			if err != nil {
				continue
			}
			read += int64(len(chunk))
			partial = append(partial, chunk...)
			for {
				end := bytes.IndexByte(partial, '\n')
				if end < 0 {
					break
				}
				line := partial[:end]
				partial = partial[end+1:]
				if !cliLogLine.Match(line) || createLine.Match(line) {
					return
				}
				environment, ok := pickedEnvironment(line)
				if !ok {
					continue
				}
				if environment != want {
					picked <- environment
				}
				return
			}
			if read > watchedBytes {
				return
			}
		}
	}()
	return picked
}

// pickedEnvironment is the environment ID a debug log line says the CLI
// picked, as in "<time> [DEBUG] Selected environment: env_01… (name,
// kind)".
func pickedEnvironment(line []byte) (string, bool) {
	match := selectedLine.FindSubmatch(line)
	if match == nil {
		return "", false
	}
	return string(match[1]), true
}

// readFrom reads what path holds past offset, up to watchedBytes.
func readFrom(path string, offset int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, watchedBytes))
}
