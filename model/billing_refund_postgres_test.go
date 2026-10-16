package model

import (
	"encoding/binary"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingRefundPostgresLostCommitAcknowledgmentDoesNotCreditTwice(t *testing.T) {
	requirePostgresAccountingTestDB(t)
	config, err := pgx.ParseConfig(os.Getenv("CILAI_TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var armed, dropped atomic.Bool
	var connections sync.Map
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Store(client, true)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer client.Close()
				defer connections.Delete(client)
				server, err := net.Dial("tcp", net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))))
				if err != nil {
					return
				}
				defer server.Close()
				connections.Store(server, true)
				defer connections.Delete(server)
				// Forward the length-prefixed startup packet, then normal typed
				// frames in both directions. Only one COMMIT acknowledgment is lost.
				header := make([]byte, 4)
				if _, err := io.ReadFull(client, header); err != nil {
					return
				}
				size := binary.BigEndian.Uint32(header)
				if size < 4 || size > 1<<20 {
					return
				}
				payload := make([]byte, size-4)
				if _, err := io.ReadFull(client, payload); err != nil {
					return
				}
				if _, err := server.Write(append(header, payload...)); err != nil {
					return
				}
				var loseCommit atomic.Bool
				frontendDone := make(chan struct{})
				go func() {
					defer close(frontendDone)
					defer server.Close()
					for {
						frame, err := readPostgresTestFrame(client)
						if err != nil {
							return
						}
						if frame[0] == 'Q' && strings.EqualFold(strings.TrimRight(string(frame[5:]), "\x00"), "commit") && armed.CompareAndSwap(true, false) {
							loseCommit.Store(true)
						}
						if _, err := server.Write(frame); err != nil {
							return
						}
					}
				}()
				for {
					frame, err := readPostgresTestFrame(server)
					if err != nil {
						break
					}
					if frame[0] == 'C' && string(frame[5:]) == "COMMIT\x00" && loseCommit.Load() {
						dropped.Store(true)
						break
					}
					if _, err := client.Write(frame); err != nil {
						break
					}
				}
				_ = client.Close()
				_ = server.Close()
				<-frontendDone
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		connections.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
		workers.Wait()
	})
	proxyURL := url.URL{Scheme: "postgres", User: url.UserPassword(config.User, config.Password), Host: listener.Addr().String(), Path: "/" + config.Database, RawQuery: "sslmode=disable"}
	proxyDB, err := testutil.OpenPostgres(proxyURL.String())
	require.NoError(t, err)
	previous := DB
	DB = proxyDB
	t.Cleanup(func() {
		DB = previous
		sqlDB, err := proxyDB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	receipt := seedWalletRefundReceipt(t)
	armed.Store(true)
	err = ProcessBillingRefund(receipt.ID)
	require.Error(t, err, "server commits, but its acknowledgment is dropped before the client sees it")
	require.True(t, dropped.Load())
	require.NoError(t, previous.First(receipt, receipt.ID).Error)
	assert.Equal(t, BillingRefundCachePending, receipt.Status)
	require.NoError(t, RecordBillingRefundFailure(receipt.ID, err))
	require.NoError(t, ProcessBillingRefund(receipt.ID))
	assert.Equal(t, 1000, getUserQuotaForPaymentGuardTest(t, 101))
	var token Token
	require.NoError(t, DB.First(&token, 101).Error)
	assert.Equal(t, 1000, token.RemainQuota)
}

func readPostgresTestFrame(connection net.Conn) ([]byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(connection, header); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size < 4 || size > 16<<20 {
		return nil, io.ErrUnexpectedEOF
	}
	payload := make([]byte, size-4)
	if _, err := io.ReadFull(connection, payload); err != nil {
		return nil, err
	}
	return append(header, payload...), nil
}
