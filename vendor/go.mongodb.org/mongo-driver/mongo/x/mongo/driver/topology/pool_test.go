package topology

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo/address"
	"go.mongodb.org/mongo-driver/x/mongo/driver/operation"
)

func TestPool(t *testing.T) {
	t.Run("newPool", func(t *testing.T) {
		t.Parallel()

		t.Run("should be paused", func(t *testing.T) {
			t.Parallel()

			p := newPool(poolConfig{})
			assert.Equalf(t, poolPaused, p.getState(), "expected new pool to be paused")

			p.close(context.Background())
		})
	})
	t.Run("closeConnection", func(t *testing.T) {
		t.Parallel()

		t.Run("can't close connection from different pool", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p1 := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p1.ready()
			noerr(t, err)

			c, err := p1.checkOut(context.Background())
			noerr(t, err)

			p2 := newPool(poolConfig{})
			err = p2.ready()
			noerr(t, err)

			err = p2.closeConnection(c)
			assert.Equalf(t, ErrWrongPool, err, "expected ErrWrongPool error")

			p1.close(context.Background())
			p2.close(context.Background())
		})
	})
	t.Run("close", func(t *testing.T) {
		t.Parallel()

		t.Run("calling close multiple times does not panic", func(t *testing.T) {
			t.Parallel()

			p := newPool(poolConfig{})
			err := p.ready()
			noerr(t, err)

			for i := 0; i < 5; i++ {
				p.close(context.Background())
			}
		})
		t.Run("closes idle connections", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			conns := make([]*connection, 3)
			for i := range conns {
				conns[i], err = p.checkOut(context.Background())
				noerr(t, err)
			}
			for i := range conns {
				err = p.checkIn(conns[i])
				noerr(t, err)
			}
			assert.Equalf(t, 3, d.lenopened(), "should have opened 3 connections")
			assert.Equalf(t, 0, d.lenclosed(), "should have closed 0 connections")
			assert.Equalf(t, 3, p.availableConnectionCount(), "should have 3 available connections")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should have 3 total connections")

			p.close(context.Background())
			assertConnectionsClosed(t, d, 3)
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 available connections")
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 total connections")
		})
		t.Run("closes all open connections", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			conns := make([]*connection, 3)
			for i := range conns {
				conns[i], err = p.checkOut(context.Background())
				noerr(t, err)
			}
			for i := 0; i < 2; i++ {
				err = p.checkIn(conns[i])
				noerr(t, err)
			}
			assert.Equalf(t, 3, d.lenopened(), "should have opened 3 connections")
			assert.Equalf(t, 0, d.lenclosed(), "should have closed 0 connections")
			assert.Equalf(t, 2, p.availableConnectionCount(), "should have 2 available connections")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should have 3 total connections")

			p.close(context.Background())
			assertConnectionsClosed(t, d, 3)
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 available connections")
			assert.Equalf(t, 0, p.totalConnectionCount(), "should have 0 total connections")
		})
		t.Run("no race if connections are also connecting", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			_, err = p.checkOut(context.Background())
			noerr(t, err)

			closed := make(chan struct{})
			started := make(chan struct{})
			go func() {
				close(started)

				for {
					select {
					case <-closed:
						return
					default:
						c, _ := p.checkOut(context.Background())
						_ = p.checkIn(c)
						time.Sleep(time.Millisecond)
					}
				}
			}()

			// Wait for the background goroutine to start running before trying to close the
			// connection pool.
			<-started
			_, err = p.checkOut(context.Background())
			noerr(t, err)

			p.close(context.Background())

			close(closed)
		})
		t.Run("shuts down gracefully if Context has a deadline", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			// Check out 2 connections from the pool and add them to a conns slice.
			conns := make([]*connection, 2)
			for i := 0; i < 2; i++ {
				c, err := p.checkOut(context.Background())
				noerr(t, err)

				conns[i] = c
			}

			// Check out a 3rd connection from the pool and immediately check it back in so there is
			// a mixture of in-use and idle connections.
			c, err := p.checkOut(context.Background())
			noerr(t, err)

			err = p.checkIn(c)
			noerr(t, err)

			// Start a goroutine that waits for the pool to start closing, then checks in the
			// 2 in-use connections. Assert that both connections are still connected during
			// graceful shutdown before they are checked in.
			go func() {
				for p.getState() == poolReady {
					time.Sleep(time.Millisecond)
				}
				for _, c := range conns {
					assert.Equalf(t, connConnected, c.state, "expected conn to still be connected")

					err := p.checkIn(c)
					noerr(t, err)
				}
			}()

			// Close the pool with a 1-hour graceful shutdown timeout. Expect that the call to
			// close() returns when all of the connections are checked in. If close() doesn't return
			// when all of the connections are checked in, the test will time out.
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
			defer cancel()
			p.close(ctx)
		})
		t.Run("closing a Connection does not cause an error after pool is closed", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			c, err := p.checkOut(context.Background())
			noerr(t, err)

			p.close(context.Background())

			c1 := &Connection{connection: c}
			err = c1.Close()
			noerr(t, err)
		})
	})
	t.Run("ready", func(t *testing.T) {
		t.Parallel()

		t.Run("can ready a paused pool", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 6, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			conns := make([]*connection, 3)
			for i := range conns {
				conn, err := p.checkOut(context.Background())
				noerr(t, err)
				conns[i] = conn
			}
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 available connections")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should have 3 total connections")

			p.clear(nil, nil)
			for _, conn := range conns {
				err = p.checkIn(conn)
				noerr(t, err)
			}
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 available connections")
			assert.Equalf(t, 0, p.totalConnectionCount(), "should have 0 total connections")

			err = p.ready()
			noerr(t, err)

			for i := 0; i < 3; i++ {
				_, err := p.checkOut(context.Background())
				noerr(t, err)
			}
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 available connections")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should have 3 total connections")

			p.close(context.Background())
		})
		t.Run("calling ready multiple times does not return an error", func(t *testing.T) {
			t.Parallel()

			p := newPool(poolConfig{})
			for i := 0; i < 5; i++ {
				err := p.ready()
				noerr(t, err)
			}

			p.close(context.Background())
		})
		t.Run("can clear and ready multiple times", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 2, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			c, err := p.checkOut(context.Background())
			noerr(t, err)
			err = p.checkIn(c)
			noerr(t, err)

			for i := 0; i < 100; i++ {
				err = p.ready()
				noerr(t, err)

				p.clear(nil, nil)
			}

			err = p.ready()
			noerr(t, err)

			c, err = p.checkOut(context.Background())
			noerr(t, err)
			err = p.checkIn(c)
			noerr(t, err)

			p.close(context.Background())
		})
		t.Run("can clear and ready multiple times concurrently", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 2, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			c, err := p.checkOut(context.Background())
			noerr(t, err)
			err = p.checkIn(c)
			noerr(t, err)

			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 1000; i++ {
						err := p.ready()
						noerr(t, err)
					}
				}()

				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 1000; i++ {
						p.clear(errors.New("test error"), nil)
					}
				}()
			}

			wg.Wait()
			err = p.ready()
			noerr(t, err)

			c, err = p.checkOut(context.Background())
			noerr(t, err)
			err = p.checkIn(c)
			noerr(t, err)

			p.close(context.Background())
		})
	})
	t.Run("checkOut", func(t *testing.T) {
		t.Parallel()

		t.Run("return error when attempting to create new connection", func(t *testing.T) {
			t.Parallel()

			dialErr := errors.New("create new connection error")
			p := newPool(poolConfig{}, WithDialer(func(Dialer) Dialer {
				return DialerFunc(func(context.Context, string, string) (net.Conn, error) {
					return nil, dialErr
				})
			}))
			err := p.ready()
			noerr(t, err)

			_, err = p.checkOut(context.Background())
			var want error = ConnectionError{Wrapped: dialErr, init: true}
			assert.Equalf(t, want, err, "should return error from calling checkOut()")
			assert.Equalf(t, 0, p.totalConnectionCount(), "pool should have 0 total connections")

			p.close(context.Background())
		})
		t.Run("closes perished connections", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 2, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(
				poolConfig{
					Address:     address.Address(addr.String()),
					MaxIdleTime: time.Millisecond,
				},
				WithDialer(func(Dialer) Dialer { return d }),
			)
			err := p.ready()
			noerr(t, err)

			// Check out a connection and assert that the idle timeout is properly set then check it
			// back into the pool.
			c1, err := p.checkOut(context.Background())
			noerr(t, err)
			assert.Equalf(t, 1, d.lenopened(), "should have opened 1 connection")
			assert.Equalf(t, 1, p.totalConnectionCount(), "pool should have 1 total connection")
			assert.Equalf(t, time.Millisecond, c1.idleTimeout, "connection should have a 1ms idle timeout")

			err = p.checkIn(c1)
			noerr(t, err)

			// Sleep for more than the 1ms idle timeout and then try to check out a connection.
			// Expect that the previously checked-out connection is closed because it's idle and a
			// new connection is created.
			time.Sleep(50 * time.Millisecond)
			c2, err := p.checkOut(context.Background())
			noerr(t, err)
			assert.NotEqualf(t, c1, c2, "expected a new connection on 2nd check out after idle timeout expires")
			assert.Equalf(t, 2, d.lenopened(), "should have opened 2 connections")
			assert.Equalf(t, 1, p.totalConnectionCount(), "pool should have 1 total connection")

			p.close(context.Background())
		})
		t.Run("recycles connections", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			for i := 0; i < 100; i++ {
				c, err := p.checkOut(context.Background())
				noerr(t, err)

				err = p.checkIn(c)
				noerr(t, err)
			}
			assert.Equalf(t, 1, d.lenopened(), "should have opened 1 connection")

			p.close(context.Background())
		})
		t.Run("cannot checkOut from closed pool", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			p.close(context.Background())

			_, err = p.checkOut(context.Background())
			assert.Equalf(
				t,
				ErrPoolClosed,
				err,
				"expected an error from checkOut() from a closed pool")
		})
		t.Run("handshaker i/o fails", func(t *testing.T) {
			t.Parallel()

			p := newPool(
				poolConfig{},
				WithHandshaker(func(Handshaker) Handshaker {
					return operation.NewHello()
				}),
				WithDialer(func(Dialer) Dialer {
					return DialerFunc(func(context.Context, string, string) (net.Conn, error) {
						return &writeFailConn{&net.TCPConn{}}, nil
					})
				}),
			)
			err := p.ready()
			noerr(t, err)

			_, err = p.checkOut(context.Background())
			assert.IsTypef(t, ConnectionError{}, err, "expected a ConnectionError")
			if err, ok := err.(ConnectionError); ok {
				assert.Containsf(
					t,
					err.Unwrap().Error(),
					"unable to write wire message to network: Write error",
					"expected error to contain string")
			}
			assert.Equalf(t, 0, p.availableConnectionCount(), "pool should have 0 available connections")
			// On connect() failure, the connection is removed and closed after delivering the error
			// to checkOut(), so it may still count toward the total connection count briefly. Wait
			// up to 100ms for the total connection count to reach 0.
			assert.Eventually(t,
				func() bool {
					return p.totalConnectionCount() == 0
				},
				100*time.Millisecond,
				1*time.Millisecond,
				"expected pool to have 0 total connections within 100ms")

			p.close(context.Background())
		})
		// Test that if a checkOut() times out, it returns a WaitQueueTimeout error that wraps a
		// context.DeadlineExceeded error.
		t.Run("wait queue timeout error", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MaxPoolSize: 1,
			})
			err := p.ready()
			noerr(t, err)

			// check out first connection.
			_, err = p.checkOut(context.Background())
			noerr(t, err)

			// Set a short timeout and check out again.
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err = p.checkOut(ctx)
			assert.NotNilf(t, err, "expected a WaitQueueTimeout error")

			// Assert that error received is WaitQueueTimeoutError with context deadline exceeded.
			assert.IsTypef(t, WaitQueueTimeoutError{}, err, "expected a WaitQueueTimeoutError")
			if err, ok := err.(WaitQueueTimeoutError); ok {
				assert.Equalf(t, context.DeadlineExceeded, err.Unwrap(), "expected wrapped error to be a context.Timeout")
			}

			p.close(context.Background())
		})
		// Test that an indefinitely blocked checkOut() doesn't cause the wait queue to overflow
		// if there are many other checkOut() calls that time out. This tests a scenario where a
		// wantConnQueue may grow unbounded while a checkOut() is blocked, even if all subsequent
		// checkOut() calls time out (due to the behavior of wantConnQueue.cleanFront()).
		t.Run("wait queue doesn't overflow", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MaxPoolSize: 1,
			})
			err := p.ready()
			noerr(t, err)

			// Check out the 1 connection that the pool will create.
			c, err := p.checkOut(context.Background())
			noerr(t, err)

			// Start a goroutine that tries to check out another connection with no timeout. Expect
			// this goroutine to block (wait in the wait queue) until the checked-out connection is
			// checked-in. Assert that there is no error once checkOut() finally does return.
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()

				_, err := p.checkOut(context.Background())
				noerr(t, err)
			}()

			// Run lots of check-out attempts with a low timeout and assert that each one fails with
			// a WaitQueueTimeout error. Expect no other errors or panics.
			for i := 0; i < 50000; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
				_, err := p.checkOut(ctx)
				cancel()
				assert.NotNilf(t, err, "expected a WaitQueueTimeout error")
				assert.IsTypef(t, WaitQueueTimeoutError{}, err, "expected a WaitQueueTimeoutError")
			}

			// Check-in the connection we checked out earlier and wait for the checkOut() goroutine
			// to resume.
			err = p.checkIn(c)
			noerr(t, err)
			wg.Wait()

			p.close(context.Background())
		})
		// Test that checkOut() on a full connection pool creates and returns a new connection
		// immediately as soon as the pool is no longer full.
		t.Run("should return a new connection as soon as the pool isn't full", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(
				poolConfig{
					Address:     address.Address(addr.String()),
					MaxPoolSize: 2,
				},
				WithDialer(func(Dialer) Dialer { return d }),
			)
			err := p.ready()
			noerr(t, err)

			// Check out two connections (MaxPoolSize) so that subsequent checkOut() calls should
			// block until a connection is checked back in or removed from the pool.
			c, err := p.checkOut(context.Background())
			noerr(t, err)
			_, err = p.checkOut(context.Background())
			noerr(t, err)
			assert.Equalf(t, 2, d.lenopened(), "should have opened 2 connection")
			assert.Equalf(t, 2, p.totalConnectionCount(), "pool should have 2 total connection")
			assert.Equalf(t, 0, p.availableConnectionCount(), "pool should have 0 idle connection")

			// Run a checkOut() with timeout and expect it to time out because the pool is at
			// MaxPoolSize and no connections are checked in or removed from the pool.
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
			defer cancel()
			_, err = p.checkOut(ctx)
			assert.Equalf(
				t,
				context.DeadlineExceeded,
				err.(WaitQueueTimeoutError).Wrapped,
				"expected wrapped error to be a context.DeadlineExceeded")

			// Start a goroutine that closes one of the checked-out conections and checks it in.
			// Expect that the checked-in connection is closed and allows blocked checkOut() to
			// complete. Assert that the time between checking in the closed connection and when the
			// checkOut() completes is within 100ms.
			var start time.Time
			go func() {
				c.close()
				start = time.Now()
				err := p.checkIn(c)
				noerr(t, err)
			}()
			_, err = p.checkOut(context.Background())
			noerr(t, err)
			assert.WithinDurationf(
				t,
				time.Now(),
				start,
				100*time.Millisecond,
				"expected checkOut to complete within 100ms of checking in a closed connection")

			assert.Equalf(t, 1, d.lenclosed(), "should have closed 1 connection")
			assert.Equalf(t, 3, d.lenopened(), "should have opened 3 connection")
			assert.Equalf(t, 2, p.totalConnectionCount(), "pool should have 2 total connection")
			assert.Equalf(t, 0, p.availableConnectionCount(), "pool should have 0 idle connection")

			p.close(context.Background())
		})
	})
	t.Run("checkIn", func(t *testing.T) {
		t.Parallel()

		t.Run("cannot return same connection to pool twice", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p.ready()
			noerr(t, err)

			c, err := p.checkOut(context.Background())
			noerr(t, err)
			assert.Equalf(t, 0, p.availableConnectionCount(), "should be no idle connections in pool")
			assert.Equalf(t, 1, p.totalConnectionCount(), "should be 1 total connection in pool")

			err = p.checkIn(c)
			noerr(t, err)

			err = p.checkIn(c)
			assert.NotNilf(t, err, "expected an error trying to return the same conn to the pool twice")

			assert.Equalf(t, 1, p.availableConnectionCount(), "should have returned 1 idle connection to the pool")
			assert.Equalf(t, 1, p.totalConnectionCount(), "should have 1 total connection in pool")

			p.close(context.Background())
		})
		t.Run("closes connections if the pool is closed", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			c, err := p.checkOut(context.Background())
			noerr(t, err)
			assert.Equalf(t, 0, d.lenclosed(), "should have closed 0 connections")
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 idle connections in pool")
			assert.Equalf(t, 1, p.totalConnectionCount(), "should have 1 total connection in pool")

			p.close(context.Background())

			err = p.checkIn(c)
			noerr(t, err)
			assert.Equalf(t, 1, d.lenclosed(), "should have closed 1 connection")
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 idle connections in pool")
			assert.Equalf(t, 0, p.totalConnectionCount(), "should have 0 total connection in pool")
		})
		t.Run("can't checkIn a connection from different pool", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			p1 := newPool(poolConfig{
				Address: address.Address(addr.String()),
			})
			err := p1.ready()
			noerr(t, err)

			c, err := p1.checkOut(context.Background())
			noerr(t, err)

			p2 := newPool(poolConfig{})
			err = p2.ready()
			noerr(t, err)

			err = p2.checkIn(c)
			assert.Equalf(t, ErrWrongPool, err, "expected ErrWrongPool error")

			p1.close(context.Background())
			p2.close(context.Background())
		})
		t.Run("bumps the connection idle deadline", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 1, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MaxIdleTime: 100 * time.Millisecond,
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)
			defer p.close(context.Background())

			c, err := p.checkOut(context.Background())
			noerr(t, err)

			// Sleep for 110ms, which will exceed the 100ms connection idle timeout. Then check the
			// connection back in and expect that it is not closed because checkIn() should bump the
			// connection idle deadline.
			time.Sleep(110 * time.Millisecond)
			err = p.checkIn(c)
			noerr(t, err)

			assert.Equalf(t, 0, d.lenclosed(), "should have closed 0 connections")
			assert.Equalf(t, 1, p.availableConnectionCount(), "should have 1 idle connections in pool")
			assert.Equalf(t, 1, p.totalConnectionCount(), "should have 1 total connection in pool")
		})
		t.Run("sets minPoolSize connection idle deadline", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 4, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MinPoolSize: 3,
				MaxIdleTime: 10 * time.Millisecond,
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)
			defer p.close(context.Background())

			// Wait for maintain() to open 3 connections.
			assertConnectionsOpened(t, d, 3)

			// Sleep for 100ms, which will exceed the 10ms connection idle timeout, then try to check
			// out a connection. Expect that all minPoolSize connections checked into the pool by
			// maintain() have passed their idle deadline, so checkOut() closes all 3 connections
			// and tries to create a new connection.
			time.Sleep(100 * time.Millisecond)
			_, err = p.checkOut(context.Background())
			noerr(t, err)

			assertConnectionsClosed(t, d, 3)
			assert.Equalf(t, 4, d.lenopened(), "should have opened 4 connections")
			assert.Equalf(t, 0, p.availableConnectionCount(), "should have 0 idle connections in pool")
			assert.Equalf(t, 1, p.totalConnectionCount(), "should have 1 total connection in pool")
		})
	})
	t.Run("maintain", func(t *testing.T) {
		t.Parallel()

		t.Run("creates MinPoolSize connections shortly after calling ready", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 3, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MinPoolSize: 3,
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			assertConnectionsOpened(t, d, 3)
			assert.Equalf(t, 3, p.availableConnectionCount(), "should be 3 idle connections in pool")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should be 3 total connection in pool")

			p.close(context.Background())
		})
		t.Run("when MinPoolSize > MaxPoolSize should not exceed MaxPoolSize connections", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 20, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MinPoolSize: 20,
				MaxPoolSize: 2,
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			assertConnectionsOpened(t, d, 2)
			assert.Equalf(t, 2, p.availableConnectionCount(), "should be 2 idle connections in pool")
			assert.Equalf(t, 2, p.totalConnectionCount(), "should be 2 total connection in pool")

			p.close(context.Background())
		})
		t.Run("removes perished connections", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 5, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address: address.Address(addr.String()),
				// Set the pool's maintain interval to 10ms so that it allows the test to run quickly.
				MaintainInterval: 10 * time.Millisecond,
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)

			// Check out and check in 3 connections. Assert that there are 3 total and 3 idle
			// connections in the pool.
			conns := make([]*connection, 3)
			for i := range conns {
				conns[i], err = p.checkOut(context.Background())
				noerr(t, err)
			}
			for _, c := range conns {
				err = p.checkIn(c)
				noerr(t, err)
			}
			assert.Equalf(t, 3, d.lenopened(), "should have opened 3 connections")
			assert.Equalf(t, 3, p.availableConnectionCount(), "should be 3 idle connections in pool")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should be 3 total connection in pool")

			// Manually make two of the connections in the idle connections stack perished due to
			// passing the connection's idle deadline. Assert that maintain() closes the two
			// perished connections and removes them from the pool.
			p.idleMu.Lock()
			for i := 0; i < 2; i++ {
				p.idleConns[i].idleTimeout = time.Millisecond
				p.idleConns[i].idleDeadline.Store(time.Now().Add(-1 * time.Hour))
			}
			p.idleMu.Unlock()
			assertConnectionsClosed(t, d, 2)
			assert.Equalf(t, 1, p.availableConnectionCount(), "should be 1 idle connections in pool")
			assert.Equalf(t, 1, p.totalConnectionCount(), "should be 1 total connection in pool")

			p.close(context.Background())
		})
		t.Run("removes perished connections and replaces them to maintain MinPoolSize", func(t *testing.T) {
			t.Parallel()

			cleanup := make(chan struct{})
			defer close(cleanup)
			addr := bootstrapConnections(t, 5, func(nc net.Conn) {
				<-cleanup
				_ = nc.Close()
			})

			d := newdialer(&net.Dialer{})
			p := newPool(poolConfig{
				Address:     address.Address(addr.String()),
				MinPoolSize: 3,
				// Set the pool's maintain interval to 10ms so that it allows the test to run quickly.
				MaintainInterval: 10 * time.Millisecond,
			}, WithDialer(func(Dialer) Dialer { return d }))
			err := p.ready()
			noerr(t, err)
			assertConnectionsOpened(t, d, 3)
			assert.Equalf(t, 3, p.availableConnectionCount(), "should be 3 idle connections in pool")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should be 3 total connection in pool")

			p.idleMu.Lock()
			for i := 0; i < 2; i++ {
				p.idleConns[i].idleTimeout = time.Millisecond
				p.idleConns[i].idleDeadline.Store(time.Now().Add(-1 * time.Hour))
			}
			p.idleMu.Unlock()
			assertConnectionsClosed(t, d, 2)
			assertConnectionsOpened(t, d, 5)
			assert.Equalf(t, 3, p.availableConnectionCount(), "should be 3 idle connections in pool")
			assert.Equalf(t, 3, p.totalConnectionCount(), "should be 3 total connection in pool")

			p.close(context.Background())
		})
	})
}

func assertConnectionsClosed(t *testing.T, dialer *dialer, count int) {
	t.Helper()

	start := time.Now()
	for {
		if dialer.lenclosed() == count {
			return
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf(
				"Waited for 3 seconds for %d connections to be closed, but got %d",
				count,
				dialer.lenclosed())
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertConnectionsOpened(t *testing.T, dialer *dialer, count int) {
	t.Helper()

	start := time.Now()
	for {
		if dialer.lenopened() == count {
			return
		}
		if time.Since(start) > 3*time.Second {
			t.Errorf(
				"Waited for 3 seconds for %d connections to be opened, but got %d",
				count,
				dialer.lenopened())
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
