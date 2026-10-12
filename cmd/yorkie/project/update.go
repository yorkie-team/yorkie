/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var (
	flagAuthWebhookURL              string
	flagAuthWebhookMethodsAdd       []string
	flagAuthWebhookMethodsRm        []string
	flagAuthWebhookMaxRetries       uint64
	flagAuthWebhookMinWaitInterval  time.Duration
	flagAuthWebhookMaxWaitInterval  time.Duration
	flagAuthWebhookRequestTimeout   time.Duration
	flagEventWebhookURL             string
	flagEventWebhookEventsAdd       []string
	flagEventWebhookEventsRm        []string
	flagEventWebhookMaxRetries      uint64
	flagEventWebhookMinWaitInterval time.Duration
	flagEventWebhookMaxWaitInterval time.Duration
	flagEventWebhookRequestTimeout  time.Duration
	flagName                        string
	flagClientDeactivateThreshold   time.Duration
	flagChannelSessionTTL           time.Duration
	flagSnapshotThreshold           int64
	flagSnapshotInterval            int64
	flagMaxSubscribersPerDocument   int
	flagMaxAttachmentsPerDocument   int
	flagRemoveOnDetach              bool
)

// allAuthWebhookMethods returns the methods 'ALL' expands to: every auth
// method except the deprecated aliases, which an enabled Watch already covers.
func allAuthWebhookMethods() []string {
	var methods []string
	for _, m := range types.AuthMethods() {
		if !m.IsDeprecated() {
			methods = append(methods, string(m))
		}
	}
	return methods
}

var allEventWebhookEvents = []string{
	string(types.DocRootChanged),
}

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "update [name]",
		Short:   "Update a project",
		Example: "yorkie project update name [options]",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("name is required")
			}
			name := args[0]

			if !hasUpdateFlag(cmd) {
				return errors.New("nothing to update: pass at least one option")
			}

			rpcAddr := viper.GetString("rpcAddr")
			auth, err := config.LoadAuth(rpcAddr)
			if err != nil {
				return err
			}

			cli, err := admin.Dial(rpcAddr, admin.WithToken(auth.Token), admin.WithInsecure(auth.Insecure))
			if err != nil {
				return err
			}
			defer func() {
				cli.Close()
			}()

			ctx := context.Background()
			project, err := cli.GetProject(ctx, name)
			if err != nil {
				return err
			}
			id := project.ID.String()

			updatableProjectFields := updatableFieldsFromFlags(cmd, project)

			updated, err := cli.UpdateProject(ctx, id, updatableProjectFields)
			if err != nil {
				if connErr, ok := errors.AsType[*connect.Error](err); ok {
					for _, detail := range connErr.Details() {
						value, err := detail.Value()
						if err != nil {
							continue
						}

						badReq, ok := value.(*errdetails.BadRequest)
						if !ok {
							continue
						}

						for _, violation := range badReq.GetFieldViolations() {
							cmd.Printf("Invalid Field: %q - %s\n", violation.GetField(), violation.GetDescription())
						}
					}
				}

				return err
			}

			output := viper.GetString("output")
			if err := printUpdateProjectInfo(cmd, output, updated); err != nil {
				return err
			}

			return nil
		},
	}
}

// hasUpdateFlag returns whether the user passed any option of this command.
func hasUpdateFlag(cmd *cobra.Command) bool {
	changed := false
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			changed = true
		}
	})
	return changed
}

// updatableFieldsFromFlags builds the fields to update from the options the
// user passed. A field whose option was not passed stays nil so the server
// keeps the stored value. Resending it instead would revalidate a value the
// user did not touch (an empty ChannelSessionTTL, which the server reads as
// its default, fails validation) and, for options whose default is not zero,
// overwrite the stored value with that default.
func updatableFieldsFromFlags(
	cmd *cobra.Command,
	project *types.Project,
) *types.UpdatableProjectFields {
	flags := cmd.Flags()
	fields := &types.UpdatableProjectFields{}

	if flags.Changed("name") {
		fields.Name = &flagName
	}

	if flags.Changed("auth-webhook-url") {
		fields.AuthWebhookURL = &flagAuthWebhookURL
	}
	if flags.Changed("auth-webhook-method-add") || flags.Changed("auth-webhook-method-rm") {
		methods := updateStringSlice(
			project.AuthWebhookMethods, // prev
			flagAuthWebhookMethodsRm,   // removes
			flagAuthWebhookMethodsAdd,  // adds
			allAuthWebhookMethods(),    // all
		)
		fields.AuthWebhookMethods = &methods
	}
	if flags.Changed("auth-webhook-max-retries") {
		fields.AuthWebhookMaxRetries = &flagAuthWebhookMaxRetries
	}
	fields.AuthWebhookMinWaitInterval = changedDuration(
		flags, "auth-webhook-min-wait-interval", flagAuthWebhookMinWaitInterval,
	)
	fields.AuthWebhookMaxWaitInterval = changedDuration(
		flags, "auth-webhook-max-wait-interval", flagAuthWebhookMaxWaitInterval,
	)
	fields.AuthWebhookRequestTimeout = changedDuration(
		flags, "auth-webhook-request-timeout", flagAuthWebhookRequestTimeout,
	)

	if flags.Changed("event-webhook-url") {
		fields.EventWebhookURL = &flagEventWebhookURL
	}
	if flags.Changed("event-webhook-events-add") || flags.Changed("event-webhook-events-rm") {
		events := updateStringSlice(
			project.EventWebhookEvents, // prev
			flagEventWebhookEventsRm,   // removes
			flagEventWebhookEventsAdd,  // adds
			allEventWebhookEvents,      // all
		)
		fields.EventWebhookEvents = &events
	}
	if flags.Changed("event-webhook-max-retries") {
		fields.EventWebhookMaxRetries = &flagEventWebhookMaxRetries
	}
	fields.EventWebhookMinWaitInterval = changedDuration(
		flags, "event-webhook-min-wait-interval", flagEventWebhookMinWaitInterval,
	)
	fields.EventWebhookMaxWaitInterval = changedDuration(
		flags, "event-webhook-max-wait-interval", flagEventWebhookMaxWaitInterval,
	)
	fields.EventWebhookRequestTimeout = changedDuration(
		flags, "event-webhook-request-timeout", flagEventWebhookRequestTimeout,
	)

	fields.ClientDeactivateThreshold = changedDuration(
		flags, "client-deactivate-threshold", flagClientDeactivateThreshold,
	)
	fields.ChannelSessionTTL = changedDuration(
		flags, "channel-session-ttl", flagChannelSessionTTL,
	)

	if flags.Changed("snapshot-threshold") {
		fields.SnapshotThreshold = &flagSnapshotThreshold
	}
	if flags.Changed("snapshot-interval") {
		fields.SnapshotInterval = &flagSnapshotInterval
	}
	if flags.Changed("max-subscribers-per-document") {
		fields.MaxSubscribersPerDocument = &flagMaxSubscribersPerDocument
	}
	if flags.Changed("max-attachments-per-document") {
		fields.MaxAttachmentsPerDocument = &flagMaxAttachmentsPerDocument
	}
	if flags.Changed("remove-on-detach") {
		fields.RemoveOnDetach = &flagRemoveOnDetach
	}

	return fields
}

// changedDuration returns the duration option as the string the server
// stores, or nil when the user did not pass it.
func changedDuration(flags *pflag.FlagSet, name string, value time.Duration) *string {
	if !flags.Changed(name) {
		return nil
	}
	s := value.String()
	return &s
}

func printUpdateProjectInfo(cmd *cobra.Command, output string, project *types.Project) error {
	switch output {
	case JSONOutput, DefaultOutput:
		encoded, err := json.Marshal(project)
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		cmd.Println(string(encoded))
	case YamlOutput:
		encoded, err := yaml.Marshal(project)
		if err != nil {
			return fmt.Errorf("marshal YAML: %w", err)
		}
		cmd.Println(string(encoded))
	default:
		return fmt.Errorf("unknown output format: %s", output)
	}

	return nil
}

// updateStringSlice updates the string slice with the given items to remove and add.
// If the item is "ALL", it will be replaced with all items.
func updateStringSlice(
	prevItems,
	itemsToRemove,
	itemsToAdd,
	allItems []string,
) []string {
	items := make(map[string]struct{})

	for _, p := range prevItems {
		items[p] = struct{}{}
	}

	for _, r := range itemsToRemove {
		if r == "ALL" {
			items = make(map[string]struct{})
		} else {
			delete(items, r)
		}
	}

	for _, a := range itemsToAdd {
		if a == "ALL" {
			for _, m := range allItems {
				items[m] = struct{}{}
			}
		} else {
			items[a] = struct{}{}
		}
	}

	updated := make([]string, 0, len(items))
	for s := range items {
		updated = append(updated, s)
	}
	slices.Sort(updated)
	return updated
}

func init() {
	SubCmd.AddCommand(newUpdateCommandWithFlags())
}

// newUpdateCommandWithFlags returns the update command with its options
// registered.
func newUpdateCommandWithFlags() *cobra.Command {
	cmd := newUpdateCommand()
	cmd.Flags().StringVar(
		&flagName,
		"name",
		"",
		"new project name",
	)
	cmd.Flags().StringVar(
		&flagAuthWebhookURL,
		"auth-webhook-url",
		"",
		"authorization-webhook update url",
	)
	cmd.Flags().StringArrayVar(
		&flagAuthWebhookMethodsAdd,
		"auth-webhook-method-add",
		[]string{},
		"authorization-webhook methods to add ('ALL' for all methods)",
	)
	cmd.Flags().StringArrayVar(
		&flagAuthWebhookMethodsRm,
		"auth-webhook-method-rm",
		[]string{},
		"authorization-webhook methods to remove ('ALL' for all methods)",
	)
	cmd.Flags().Uint64Var(
		&flagAuthWebhookMaxRetries,
		"auth-webhook-max-retries",
		database.DefaultAuthWebhookMaxRetries,
		"Maximum number of retries for authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&flagAuthWebhookMinWaitInterval,
		"auth-webhook-min-wait-interval",
		database.DefaultAuthWebhookMinWaitInterval,
		"Minimum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagAuthWebhookMaxWaitInterval,
		"auth-webhook-max-wait-interval",
		database.DefaultAuthWebhookMaxWaitInterval,
		"Maximum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagAuthWebhookRequestTimeout,
		"auth-webhook-request-timeout",
		database.DefaultAuthWebhookRequestTimeout,
		"Timeout for each authorization webhook request.",
	)
	cmd.Flags().StringVar(
		&flagEventWebhookURL,
		"event-webhook-url",
		"",
		"event-webhook update url",
	)
	cmd.Flags().StringArrayVar(
		&flagEventWebhookEventsAdd,
		"event-webhook-events-add",
		[]string{},
		"event-webhook events to add ('ALL' for all events)",
	)
	cmd.Flags().StringArrayVar(
		&flagEventWebhookEventsRm,
		"event-webhook-events-rm",
		[]string{},
		"event-webhook events to remove ('ALL' for all events)",
	)
	cmd.Flags().Uint64Var(
		&flagEventWebhookMaxRetries,
		"event-webhook-max-retries",
		database.DefaultEventWebhookMaxRetries,
		"Maximum number of retries for event webhook.",
	)
	cmd.Flags().DurationVar(
		&flagEventWebhookMinWaitInterval,
		"event-webhook-min-wait-interval",
		database.DefaultEventWebhookMinWaitInterval,
		"Minimum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagEventWebhookMaxWaitInterval,
		"event-webhook-max-wait-interval",
		database.DefaultEventWebhookMaxWaitInterval,
		"Maximum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagEventWebhookRequestTimeout,
		"event-webhook-request-timeout",
		database.DefaultEventWebhookRequestTimeout,
		"Timeout for each event webhook request.",
	)
	cmd.Flags().DurationVar(
		&flagClientDeactivateThreshold,
		"client-deactivate-threshold",
		database.DefaultClientDeactivateThreshold,
		"client deactivate threshold for housekeeping",
	)
	cmd.Flags().DurationVar(
		&flagChannelSessionTTL,
		"channel-session-ttl",
		database.DefaultChannelSessionTTL,
		"Channel session TTL (must be between 1s and 5m)",
	)
	cmd.Flags().Int64Var(
		&flagSnapshotThreshold,
		"snapshot-threshold",
		database.DefaultSnapshotThreshold,
		"Threshold that determines if changes should be sent with snapshot when the number "+
			"of changes is greater than this value.",
	)
	cmd.Flags().Int64Var(
		&flagSnapshotInterval,
		"snapshot-interval",
		database.DefaultSnapshotInterval,
		"Interval of changes to create a snapshot.",
	)
	cmd.Flags().IntVar(
		&flagMaxSubscribersPerDocument,
		"max-subscribers-per-document",
		0,
		"max subscribers per document",
	)
	cmd.Flags().IntVar(
		&flagMaxAttachmentsPerDocument,
		"max-attachments-per-document",
		0,
		"max attachments per document",
	)
	cmd.Flags().BoolVar(
		&flagRemoveOnDetach,
		"remove-on-detach",
		false,
		"remove on detach",
	)
	return cmd
}
