// Copyright 2017 The Cockroach Authors.
//
// Licensed as a CockroachDB Enterprise file under the Cockroach Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//     https://github.com/cockroachdb/cockroach/blob/master/licenses/CCL.txt

package partitionccl

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/config/zonepb"
	"github.com/cockroachdb/cockroach/pkg/keys"
	"github.com/cockroachdb/cockroach/pkg/server"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/sql"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/catalogkeys"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/desctestutils"
	"github.com/cockroachdb/cockroach/pkg/sql/lexbase"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/encoding"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
	"github.com/cockroachdb/redact"
	"github.com/stretchr/testify/require"
)

func TestValidIndexPartitionSetShowZones(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	s, db, _ := serverutils.StartServer(t, base.TestServerArgs{
		DefaultTestTenant: base.TODOTestTenantDisabled,
	})
	defer s.Stopper().Stop(context.Background())

	sqlDB := sqlutils.MakeSQLRunner(db)
	sqlDB.Exec(t, `
		CREATE DATABASE d;
		USE d;
		CREATE TABLE t (c STRING PRIMARY KEY) PARTITION BY LIST (c) (
			PARTITION p0 VALUES IN ('a'),
			PARTITION p1 VALUES IN (DEFAULT)
		)`)

	yamlDefault := fmt.Sprintf("gc: {ttlseconds: %d}", s.(*server.TestServer).Cfg.DefaultZoneConfig.GC.TTLSeconds)
	yamlOverride := "gc: {ttlseconds: 42}"
	zoneOverride := s.(*server.TestServer).Cfg.DefaultZoneConfig
	zoneOverride.GC = &zonepb.GCPolicy{TTLSeconds: 42}
	partialZoneOverride := *zonepb.NewZoneConfig()
	partialZoneOverride.GC = &zonepb.GCPolicy{TTLSeconds: 42}

	dbID := sqlutils.QueryDatabaseID(t, db, "d")
	tableID := sqlutils.QueryTableID(t, db, "d", "public", "t")

	defaultRow := sqlutils.ZoneRow{
		ID:     keys.RootNamespaceID,
		Config: s.(*server.TestServer).Cfg.DefaultZoneConfig,
	}
	defaultOverrideRow := sqlutils.ZoneRow{
		ID:     keys.RootNamespaceID,
		Config: zoneOverride,
	}
	dbRow := sqlutils.ZoneRow{
		ID:     dbID,
		Config: zoneOverride,
	}
	tableRow := sqlutils.ZoneRow{
		ID:     tableID,
		Config: zoneOverride,
	}
	primaryRow := sqlutils.ZoneRow{
		ID:     tableID,
		Config: zoneOverride,
	}
	p0Row := sqlutils.ZoneRow{
		ID:     tableID,
		Config: zoneOverride,
	}
	p1Row := sqlutils.ZoneRow{
		ID:     tableID,
		Config: zoneOverride,
	}

	// Partially filled config rows
	partialDbRow := sqlutils.ZoneRow{
		ID:     dbID,
		Config: partialZoneOverride,
	}
	partialTableRow := sqlutils.ZoneRow{
		ID:     tableID,
		Config: partialZoneOverride,
	}
	partialPrimaryRow := sqlutils.ZoneRow{
		ID:     tableID,
		Config: partialZoneOverride,
	}
	partialP0Row := sqlutils.ZoneRow{
		ID:     tableID,
		Config: partialZoneOverride,
	}
	partialP1Row := sqlutils.ZoneRow{
		ID:     tableID,
		Config: partialZoneOverride,
	}

	// Remove stock zone configs installed at cluster bootstrap. Otherwise this
	// test breaks whenever these stock zone configs are adjusted.
	sqlutils.RemoveAllZoneConfigs(t, sqlDB)

	// Ensure the default is reported for all zones at first.
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "RANGE default", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", defaultRow)

	// Ensure a database zone config applies to that database, its tables, and its
	// tables' indices and partitions.
	sqlutils.SetZoneConfig(t, sqlDB, "DATABASE d", yamlOverride)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow, partialDbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", dbRow)

	// Ensure a table zone config applies to that table and its indices and
	// partitions, but no other zones.
	sqlutils.SetZoneConfig(t, sqlDB, "TABLE d.t", yamlOverride)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow, partialDbRow, partialTableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", tableRow)

	// Ensure an index zone config applies to that index and its partitions, but
	// no other zones.
	sqlutils.SetZoneConfig(t, sqlDB, "INDEX d.t@t_pkey", yamlOverride)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow, partialDbRow, partialTableRow, partialPrimaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", primaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", primaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", primaryRow)

	// Ensure a partition zone config applies to that partition, but no other
	// zones.
	sqlutils.SetZoneConfig(t, sqlDB, "PARTITION p0 OF TABLE d.t", yamlOverride)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow, partialDbRow, partialTableRow, partialPrimaryRow, partialP0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", primaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", p0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", primaryRow)

	// Ensure updating the default zone propagates to zones without an override,
	// but not to those with overrides.
	sqlutils.SetZoneConfig(t, sqlDB, "RANGE default", yamlOverride)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultOverrideRow, partialDbRow, partialTableRow, partialPrimaryRow, partialP0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", dbRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", primaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", p0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", primaryRow)

	// Ensure deleting a database zone leaves child overrides in place.
	sqlutils.DeleteZoneConfig(t, sqlDB, "DATABASE d")
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultOverrideRow, partialTableRow, partialPrimaryRow, partialP0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", defaultOverrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", tableRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", primaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", p0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", primaryRow)

	// Ensure deleting a table zone leaves child overrides in place.
	sqlutils.DeleteZoneConfig(t, sqlDB, "TABLE d.t")
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultOverrideRow, partialPrimaryRow, partialP0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", defaultOverrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", primaryRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", p0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", primaryRow)

	// Ensure deleting an index zone leaves child overrides in place.
	sqlutils.DeleteZoneConfig(t, sqlDB, "INDEX d.t@t_pkey")
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultOverrideRow, partialP0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", defaultOverrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", p0Row)

	// Ensure deleting a partition zone works.
	sqlutils.DeleteZoneConfig(t, sqlDB, "PARTITION p0 OF TABLE d.t")
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultOverrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", defaultOverrideRow)

	// Ensure deleting non-overridden zones is not an error.
	sqlutils.DeleteZoneConfig(t, sqlDB, "DATABASE d")
	sqlutils.DeleteZoneConfig(t, sqlDB, "TABLE d.t")
	sqlutils.DeleteZoneConfig(t, sqlDB, "PARTITION p1 OF TABLE d.t")

	// Ensure updating the default zone config applies to zones that have had
	// overrides added and removed.
	sqlutils.SetZoneConfig(t, sqlDB, "RANGE default", yamlDefault)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "RANGE default", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "DATABASE d", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "INDEX d.t@t_pkey", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", defaultRow)

	// Ensure subzones can be created even when no table zone exists.
	sqlutils.SetZoneConfig(t, sqlDB, "PARTITION p0 OF TABLE d.t", yamlOverride)
	sqlutils.SetZoneConfig(t, sqlDB, "PARTITION p1 OF TABLE d.t", yamlOverride)
	sqlutils.VerifyAllZoneConfigs(t, sqlDB, defaultRow, partialP0Row, partialP1Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "TABLE d.t", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE d.t", p0Row)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p1 OF TABLE d.t", p1Row)

	// Ensure the shorthand index syntax works.
	sqlutils.SetZoneConfig(t, sqlDB, `INDEX "t_pkey"`, yamlOverride)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, `INDEX "t_pkey"`, primaryRow)

	// Ensure the session database is respected.
	sqlutils.SetZoneConfig(t, sqlDB, "PARTITION p0 OF TABLE t", yamlOverride)
	sqlutils.VerifyZoneConfigForTarget(t, sqlDB, "PARTITION p0 OF TABLE t", p0Row)
}

func TestInvalidIndexPartitionSetShowZones(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	s, db, _ := serverutils.StartServer(t, base.TestServerArgs{
		DefaultTestTenant: base.TODOTestTenantDisabled,
	})
	defer s.Stopper().Stop(context.Background())

	for i, tc := range []struct {
		query string
		err   string
	}{
		{
			"ALTER INDEX foo CONFIGURE ZONE USING DEFAULT",
			`index "foo" does not exist`,
		},
		{
			"SHOW ZONE CONFIGURATION FOR INDEX foo",
			`index "foo" does not exist`,
		},
		{
			"USE system; ALTER INDEX foo CONFIGURE ZONE USING DEFAULT",
			`index "foo" does not exist`,
		},
		{
			"USE system; SHOW ZONE CONFIGURATION FOR INDEX foo",
			`index "foo" does not exist`,
		},
		{
			"ALTER PARTITION p0 OF TABLE system.jobs CONFIGURE ZONE = 'foo'",
			`partition "p0" does not exist`,
		},
		{
			"SHOW ZONE CONFIGURATION FOR PARTITION p0 OF TABLE system.jobs",
			`partition "p0" does not exist`,
		},
	} {
		if _, err := db.Exec(tc.query); !testutils.IsError(err, tc.err) {
			t.Errorf("#%d: expected error matching %q, but got %v", i, tc.err, err)
		}
	}
}

func TestGenerateSubzoneSpans(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	rng, _ := randutil.NewTestRand()

	partitioningTests := allPartitioningTests(rng)
	for _, test := range partitioningTests {
		if test.generatedSpans == nil {
			// The randomized partition tests don't have generatedSpans, and
			// wouldn't be very interesting to test.
			continue
		}
		t.Run(test.name, func(t *testing.T) {
			if err := test.parse(); err != nil {
				t.Fatalf("%+v", err)
			}
			clusterID := uuid.MakeV4()
			hasNewSubzones := false
			spans, err := sql.GenerateSubzoneSpans(
				cluster.NoSettings, clusterID, keys.SystemSQLCodec, test.parsed.tableDesc, test.parsed.subzones, hasNewSubzones)
			if err != nil {
				t.Fatalf("generating subzone spans: %+v", err)
			}

			var actual []string
			for _, span := range spans {
				subzone := test.parsed.subzones[span.SubzoneIndex]
				idx, err := catalog.MustFindIndexByID(test.parsed.tableDesc, descpb.IndexID(subzone.IndexID))
				if err != nil {
					t.Fatalf("could not find index with ID %d: %+v", subzone.IndexID, err)
				}

				directions := []encoding.Direction{encoding.Ascending /* index ID */}
				for i := 0; i < idx.NumKeyColumns(); i++ {
					cd := idx.GetKeyColumnDirection(i)
					ed, err := catalogkeys.IndexColumnEncodingDirection(cd)
					if err != nil {
						t.Fatal(err)
					}
					directions = append(directions, ed)
				}

				var subzoneShort string
				if len(subzone.PartitionName) > 0 {
					subzoneShort = "." + subzone.PartitionName
				} else {
					subzoneShort = "@" + idx.GetName()
				}

				// Verify that we're always doing the space savings when we can.
				var buf redact.StringBuilder
				var buf2 redact.StringBuilder
				if span.Key.PrefixEnd().Equal(span.EndKey) {
					encoding.PrettyPrintValue(&buf, directions, span.Key, "/")
					encoding.PrettyPrintValue(&buf2, directions, span.EndKey, "/")
					t.Errorf("endKey should be omitted when equal to key.PrefixEnd [%s, %s)",
						buf.String(),
						buf2.String())
				}
				if len(span.EndKey) == 0 {
					span.EndKey = span.Key.PrefixEnd()
				}

				// TODO(dan): Check that spans are sorted.
				encoding.PrettyPrintValue(&buf, directions, span.Key, "/")
				encoding.PrettyPrintValue(&buf2, directions, span.EndKey, "/")

				actual = append(actual, fmt.Sprintf("%s %s-%s", subzoneShort,
					buf.String(),
					buf2.String()))
			}

			if len(actual) != len(test.parsed.generatedSpans) {
				t.Fatalf("got \n    %v\n expected \n    %v", actual, test.generatedSpans)
			}
			for i := range actual {
				if expected := strings.TrimSpace(test.parsed.generatedSpans[i]); actual[i] != expected {
					t.Errorf("%d: got [%s] expected [%s]", i, actual[i], expected)
				}
			}
		})
	}
}

func TestZoneConfigAppliesToTemporaryIndex(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	yamlOverride := "gc: {ttlseconds: 42}"

	errCh := make(chan error)
	startIndexMerge := make(chan interface{})
	atIndexMerge := make(chan interface{})
	var params base.TestServerArgs
	params.Knobs = base.TestingKnobs{
		SQLSchemaChanger: &sql.SchemaChangerTestingKnobs{
			RunBeforeTempIndexMerge: func() {
				atIndexMerge <- struct{}{}
				<-startIndexMerge
			},
		},
	}

	s, sqlDB, kvDB := serverutils.StartServer(t, params)
	defer s.Stopper().Stop(context.Background())
	tdb := sqlutils.MakeSQLRunner(sqlDB)
	codec := s.ApplicationLayer().Codec()
	sv := &s.ApplicationLayer().ClusterSettings().SV
	sql.SecondaryTenantZoneConfigsEnabled.Override(context.Background(), sv, true)

	if _, err := sqlDB.Exec(`
SET use_declarative_schema_changer='off';
CREATE DATABASE t;
CREATE TABLE t.test (k INT PRIMARY KEY, v INT);`); err != nil {
		t.Fatal(err)
	}

	defaultRow := sqlutils.ZoneRow{
		ID:     keys.RootNamespaceID,
		Config: s.(*server.TestServer).Cfg.DefaultZoneConfig,
	}

	tableID := sqlutils.QueryTableID(t, sqlDB, "t", "public", "test")
	zoneOverride := s.(*server.TestServer).Cfg.DefaultZoneConfig
	zoneOverride.GC = &zonepb.GCPolicy{TTLSeconds: 42}

	overrideRow := sqlutils.ZoneRow{
		ID:     tableID,
		Config: zoneOverride,
	}

	sqlutils.RemoveAllZoneConfigs(t, tdb)

	// Ensure the default is reported for all zones at first.
	sqlutils.VerifyAllZoneConfigs(t, tdb, defaultRow)

	go func() {
		_, err := sqlDB.Exec(`
CREATE INDEX idx ON t.test (v) PARTITION BY LIST (v) (
PARTITION p0 VALUES IN (1),
PARTITION p1 VALUES IN (DEFAULT));`)
		errCh <- err
	}()

	<-atIndexMerge

	// Find the temporary index corresponding to the new index.
	tbl := desctestutils.TestingGetPublicTableDescriptor(kvDB, codec, "t", "test")
	newIndex, err := catalog.MustFindIndexByName(tbl, "idx")
	if err != nil {
		t.Fatal(err)
	}
	tempIndex := catalog.FindCorrespondingTemporaryIndexByID(tbl, newIndex.GetID())
	require.NotNil(t, tempIndex)

	// Test that partition targeting changes the zone config for both the new
	// index and temp index.
	tdb.Exec(t, fmt.Sprintf("ALTER %s CONFIGURE ZONE = %s", "PARTITION p0 OF TABLE t.test", lexbase.EscapeSQLString(yamlOverride)))

	sqlutils.VerifyZoneConfigForTarget(t, tdb, "PARTITION p0 OF INDEX t.test@idx", overrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, fmt.Sprintf("PARTITION p0 OF INDEX t.test@%s", tempIndex.GetName()), overrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, "PARTITION p1 OF INDEX t.test@idx", defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, fmt.Sprintf("PARTITION p1 OF INDEX t.test@%s", tempIndex.GetName()), defaultRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, "TABLE t.test", defaultRow)

	// Test that index targeting changes the zone config for both the new index
	// and the temp index.
	tdb.Exec(t, fmt.Sprintf("ALTER %s CONFIGURE ZONE = %s", "INDEX t.test@idx", lexbase.EscapeSQLString(yamlOverride)))

	sqlutils.VerifyZoneConfigForTarget(t, tdb, "PARTITION p0 OF INDEX t.test@idx", overrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, fmt.Sprintf("PARTITION p0 OF INDEX t.test@%s", tempIndex.GetName()), overrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, "PARTITION p1 OF INDEX t.test@idx", overrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, fmt.Sprintf("PARTITION p1 OF INDEX t.test@%s", tempIndex.GetName()), overrideRow)
	sqlutils.VerifyZoneConfigForTarget(t, tdb, "TABLE t.test", defaultRow)

	startIndexMerge <- struct{}{}

	err = <-errCh
	if err != nil {
		t.Fatal(err)
	}
}
