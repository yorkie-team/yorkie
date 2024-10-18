/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	v060 "github.com/yorkie-team/yorkie/migrations/v0.6.0"
	yorkiemongo "github.com/yorkie-team/yorkie/server/backend/database/mongo"
)

var (
	from         string
	to           string
	databaseName string
	batchSize    int
)

// migrationMap is a map of migration functions for each version.
var migrationMap = map[string]func(ctx context.Context, db *mongo.Client, dbName string, batchSize int) error{
	"v0.6.0": v060.RunMigration,
}

// runMigration runs the migration for the given version.
func runMigration(ctx context.Context, db *mongo.Client, version string, dbName string, batchSize int) error {
	migrationFunc, exists := migrationMap[version]
	if !exists {
		fmt.Printf("migration not found for version: %s\n", version)
		return nil
	}

	if err := migrationFunc(ctx, db, dbName, batchSize); err != nil {
		return err
	}

	return nil
}

// filterDirectories filters directories that are between the given versions.
func filterDirectories(dirPath, from, to string) ([]string, error) {
	var validDirs []string
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("no such directory %s: %w", dirPath, err)
	}

	for _, file := range files {
		if !file.IsDir() || !strings.HasPrefix(file.Name(), "v") {
			continue
		}

		version := file.Name()
		cmpFrom, err := compareVersions(version, from)
		if err != nil {
			return nil, err
		}

		cmpTo, err := compareVersions(version, to)
		if err != nil {
			return nil, err
		}

		if cmpFrom >= 0 && cmpTo <= 0 {
			validDirs = append(validDirs, version)
		}
	}
	return validDirs, nil
}

// parseVersion parses the version string into an array of integers.
func parseVersion(version string) ([]int, error) {
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	result := make([]int, len(parts))

	for i, part := range parts {
		num, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid version format: %s", version)
		}

		result[i] = num
	}

	return result, nil
}

// compareVersions compares two version strings.
func compareVersions(v1, v2 string) (int, error) {
	parsedV1, err := parseVersion(v1)
	if err != nil {
		return 0, err
	}

	parsedV2, err := parseVersion(v2)
	if err != nil {
		return 0, err
	}

	for i := 0; i < len(parsedV1) && i < len(parsedV2); i++ {
		if parsedV1[i] < parsedV2[i] {
			return -1, nil
		} else if parsedV1[i] > parsedV2[i] {
			return 1, nil
		}
	}

	if len(parsedV1) < len(parsedV2) {
		return -1, nil
	} else if len(parsedV1) > len(parsedV2) {
		return 1, nil
	}

	return 0, nil
}

func newMigrationCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "migration",
		Short:   "Run MongoDB migration",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := compareVersions(from, to)
			if err != nil {
				return err
			}

			if result == 1 {
				return fmt.Errorf("to version must be larger then from version: %s %s", from, to)
			}

			migrationDir := "../migrations"
			absMigrationDir, err := filepath.Abs(migrationDir)
			if err != nil {
				return fmt.Errorf("error converting to absolute path: %s", err)
			}

			validDirs, err := filterDirectories(absMigrationDir, from, to)
			if err != nil {
				return err
			}

			ctx := context.Background()
			client, err := mongo.Connect(
				ctx,
				options.Client().
					ApplyURI(mongoConnectionURI).
					SetRegistry(yorkiemongo.NewRegistryBuilder().Build()),
			)
			if err != nil {
				return fmt.Errorf("connect to mongo: %w", err)
			}

			databases, err := client.ListDatabaseNames(ctx, bson.M{})
			if err != nil {
				return err
			}

			databaseExists := false
			for _, dbName := range databases {
				if dbName == databaseName {
					databaseExists = true
					break
				}
			}

			if !databaseExists {
				return fmt.Errorf("database %s not found", databaseName)
			}

			for _, dir := range validDirs {
				fmt.Printf("running migration for directory: %s\n", dir)

				if err := runMigration(ctx, client, dir, databaseName, batchSize); err != nil {
					return fmt.Errorf("migration failed: %w\n", err)
				}
			}

			return nil
		},
	}
}

func init() {
	cmd := newMigrationCmd()
	cmd.Flags().StringVar(
		&mongoConnectionURI,
		"mongo-connection-uri",
		"",
		"MongoDB's connection URI",
	)
	cmd.Flags().StringVar(
		&from,
		"from",
		"",
		"starting version of migration (e.g., v0.5.1)",
	)
	cmd.Flags().StringVar(
		&to,
		"to",
		"",
		"ending version of migration (e.g., v0.6.0)",
	)
	cmd.Flags().IntVar(
		&batchSize,
		"batch-size",
		1000,
		"batch size of migration",
	)
	cmd.Flags().StringVar(
		&databaseName,
		"database-name",
		"yorkie-meta",
		"name of migration target database")
	_ = cmd.MarkFlagRequired("mongo-connection-uri")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	rootCmd.AddCommand(cmd)
}
