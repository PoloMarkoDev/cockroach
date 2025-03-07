// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver/closedts"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/storage"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedSettingsStoreAndLoad(t *testing.T) {
	defer leaktest.AfterTest(t)()

	var testSettings []roachpb.KeyValue
	for i := 0; i < 5; i++ {
		testKey := fmt.Sprintf("key_%d", i)
		testVal := fmt.Sprintf("val_%d", i)
		testSettings = append(testSettings, roachpb.KeyValue{
			Key:   []byte(testKey),
			Value: roachpb.MakeValueFromString(testVal),
		})
	}

	ctx := context.Background()
	engine, err := storage.Open(ctx, storage.InMemory(),
		cluster.MakeClusterSettings(),
		storage.MaxSize(512<<20 /* 512 MiB */),
		storage.ForTesting)
	require.NoError(t, err)
	defer engine.Close()

	require.NoError(t, storeCachedSettingsKVs(ctx, engine, testSettings))

	actualSettings, err := loadCachedSettingsKVs(ctx, engine)
	require.NoError(t, err)
	require.Equal(t, testSettings, actualSettings)
}

func TestCachedSettingsServerRestart(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	stickyEngineRegistry := NewStickyInMemEnginesRegistry()
	defer stickyEngineRegistry.CloseAllStickyInMemEngines()

	serverArgs := base.TestServerArgs{
		StoreSpecs: []base.StoreSpec{
			{InMemory: true, StickyInMemoryEngineID: "1"},
		},
		Knobs: base.TestingKnobs{
			Server: &TestingKnobs{
				StickyEngineRegistry: stickyEngineRegistry,
			},
		},
	}
	var settingsCache []roachpb.KeyValue
	testServer := serverutils.StartServerOnly(t, serverArgs)
	closedts.TargetDuration.Override(ctx, &testServer.ClusterSettings().SV, 10*time.Millisecond)
	closedts.SideTransportCloseInterval.Override(ctx, &testServer.ClusterSettings().SV, 10*time.Millisecond)
	testutils.SucceedsSoon(t, func() error {
		store, err := testServer.GetStores().(*kvserver.Stores).GetStore(1)
		if err != nil {
			return err
		}
		settings, err := loadCachedSettingsKVs(context.Background(), store.TODOEngine())
		if err != nil {
			return err
		}
		if len(settings) == 0 {
			return errors.New("empty settings loaded from store")
		}
		settingsCache = settings
		return nil
	})
	testServer.Stopper().Stop(context.Background())

	ts, err := serverutils.NewServer(serverArgs)
	if err != nil {
		t.Fatal(err)
	}
	srv := ts.(*TestServer)
	defer srv.Stopper().Stop(context.Background())

	s := srv.Server
	var initServer *initServer
	{
		getDialOpts := s.rpcContext.GRPCDialOptions

		initConfig := newInitServerConfig(ctx, s.cfg, getDialOpts)
		inspectState, err := inspectEngines(
			context.Background(),
			s.engines,
			s.cfg.Settings.Version.BinaryVersion(),
			s.cfg.Settings.Version.BinaryMinSupportedVersion(),
		)
		require.NoError(t, err)

		initServer = newInitServer(s.cfg.AmbientCtx, inspectState, initConfig)
	}

	// ServeAndWait should return immediately since the server is already initialized
	// and thus we can verify if the initial state of the server stores the same settings
	// KVs as the ones loaded with loadCachedSettingsKVs, i.e., cached on the local store.
	testutils.SucceedsSoon(t, func() error {
		state, initialBoot, err := initServer.ServeAndWait(
			context.Background(),
			s.stopper,
			&s.cfg.Settings.SV,
		)
		if err != nil {
			return err
		}
		if initialBoot {
			return errors.New("server should not require initialization")
		}
		if !assert.ObjectsAreEqual(state.initialSettingsKVs, settingsCache) {
			return errors.Newf(`initial state settings KVs does not match expected settings
Expected: %+v
Actual:   %+v
`, settingsCache, state.initialSettingsKVs)
		}
		return nil
	})
}
