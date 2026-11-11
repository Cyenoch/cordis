package loader

// SetWrite replaces the function that writes configuration files, so that
// tests can make writes fail.
func SetWrite(l *Loader, write func(path string, data []byte) error) {
	if write == nil {
		write = writeFile
	}
	l.mu.Lock()
	l.write = write
	l.mu.Unlock()
}
