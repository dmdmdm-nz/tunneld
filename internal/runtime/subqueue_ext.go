package runtime

// OutOfBandSnapshotSend pushes a message directly to the subscriber channel,
// bypassing the queue. Use ONLY during snapshot emission while the sub is paused
// and the channel was created with enough buffer to accommodate the snapshot burst.
func (sq *SubQueue[T]) OutOfBandSnapshotSend(ev T) {
	sq.outCh <- ev
}
