package export

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// The two cmd/go invocations independently discover the current source tree.
// Cancel a sibling on failure and join both processes before returning.
func discover(dir string, env []string, goBin string, listArgs, buildArgs []string) ([]byte, []byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		index int
		data  []byte
		err   error
	}
	results := make(chan result, 2)
	for i, args := range [][]string{listArgs, buildArgs} {
		go func(i int, args []string) {
			c := exec.CommandContext(ctx, goBin, args...)
			c.Dir, c.Env = dir, env
			var stdout, stderr bytes.Buffer
			c.Stdout, c.Stderr = &stdout, &stderr
			err := c.Run()
			if err != nil {
				err = fmt.Errorf("go %s planning: %w\n%s", args[0], err, stderr.String())
			}
			data := stdout.Bytes()
			if i == 1 {
				data = stderr.Bytes()
			}
			results <- result{i, data, err}
		}(i, args)
	}
	var data [2][]byte
	var firstErr error
	for i := 0; i < 2; i++ {
		r := <-results
		data[r.index] = r.data
		if r.err != nil && firstErr == nil {
			firstErr = r.err
			cancel()
		}
	}
	return data[0], data[1], firstErr
}
