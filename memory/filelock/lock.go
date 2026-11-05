package filelock

// Lock takes WithLock's lock on path and holds it until release is called, for a caller whose
// locked work does not fit in one function.
func Lock(path string) (release func(), err error) {
	acquired := make(chan error, 1)
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		if err := WithLock(path, func() error {
			acquired <- nil
			<-done
			return nil
		}); err != nil {
			acquired <- err
		}
	}()
	if err := <-acquired; err != nil {
		<-finished
		return nil, err
	}
	return func() { close(done); <-finished }, nil
}
