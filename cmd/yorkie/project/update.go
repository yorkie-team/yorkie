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
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	flagAuthWebhookURL              string
	flagAuthWebhookMethodsAdd       []string
	flagAuthWebhookMethodsRm        []string
	flagAuthWebhookMaxRetries       uint64
	flagAuthWebhookMinWaitInterval  time.Duration
	flagAuthWebhookMaxWaitInterval  time.Duration
	flagEventWebhookURL             string
	flagEventWebhookEventsAdd       []string
	flagEventWebhookEventsRm        []string
	flagEventWebhookMaxRetries      uint64
	flagEventWebhookMinWaitInterval time.Duration
	flagEventWebhookMaxWaitInterval time.Duration
	flagName                        string
	flagClientDeactivateThreshold   time.Duration
	flagMaxSubscribersPerDocument   int
	flagMaxAttachmentsPerDocument   int
	flagRemoveOnDetach              bool
)

var allAuthWebhookMethods = []string{
	string(types.ActivateClient),
	string(types.DeactivateClient),
	string(types.AttachDocument),
	string(types.DetachDocument),
	string(types.RemoveDocument),
	string(types.PushPull),
	string(types.WatchDocuments),
	string(types.Broadcast),
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

			newName := name
			if flagName != "" {
				newName = flagName
			}

			newAuthWebhookURL := project.AuthWebhookURL
			if cmd.Flags().Lookup("auth-webhook-url").Changed { // allow empty string
				newAuthWebhookURL = flagAuthWebhookURL
			}

			newAuthWebhookMethods := updateStringSlice(
				project.AuthWebhookMethods, // prev
				flagAuthWebhookMethodsRm,   // removes
				flagAuthWebhookMethodsAdd,  // adds
				allAuthWebhookMethods,      // all
			)

			newAuthWebhookMaxRetries := project.AuthWebhookMaxRetries
			if flagAuthWebhookMaxRetries != 0 {
				newAuthWebhookMaxRetries = flagAuthWebhookMaxRetries
			}
			newAuthWebhookMinWaitInterval := project.AuthWebhookMinWaitInterval
			if flagAuthWebhookMinWaitInterval != 0 {
				newAuthWebhookMinWaitInterval = flagAuthWebhookMinWaitInterval.String()
			}
			newAuthWebhookMaxWaitInterval := project.AuthWebhookMaxWaitInterval
			if flagAuthWebhookMaxWaitInterval != 0 {
				newAuthWebhookMaxWaitInterval = flagAuthWebhookMaxWaitInterval.String()
			}

			newEventWebhookURL := project.EventWebhookURL
			if cmd.Flags().Lookup("event-webhook-url").Changed { // allow empty string
				newEventWebhookURL = flagEventWebhookURL
			}

			newEventWebhookEvents := updateStringSlice(
				project.EventWebhookEvents, // prev
				flagEventWebhookEventsRm,   // removes
				flagEventWebhookEventsAdd,  // adds
				allEventWebhookEvents,      // all
			)

			newEventWebhookMaxRetries := project.EventWebhookMaxRetries
			if flagEventWebhookMaxRetries != 0 {
				newEventWebhookMaxRetries = flagEventWebhookMaxRetries
			}
			newEventWebhookMinWaitInterval := project.EventWebhookMinWaitInterval
			if flagEventWebhookMinWaitInterval != 0 {
				newEventWebhookMinWaitInterval = flagEventWebhookMinWaitInterval.String()
			}
			newEventWebhookMaxWaitInterval := project.EventWebhookMaxWaitInterval
			if flagEventWebhookMaxWaitInterval != 0 {
				newEventWebhookMaxWaitInterval = flagEventWebhookMaxWaitInterval.String()
			}

			newClientDeactivateThreshold := project.ClientDeactivateThreshold
			if flagClientDeactivateThreshold != 0 {
				newClientDeactivateThreshold = flagClientDeactivateThreshold.String()
			}

			newMaxSubscribersPerDocument := project.MaxSubscribersPerDocument
			if flagMaxSubscribersPerDocument != 0 {
				newMaxSubscribersPerDocument = flagMaxSubscribersPerDocument
			}

			newMaxAttachmentsPerDocument := project.MaxAttachmentsPerDocument
			if flagMaxAttachmentsPerDocument != 0 {
				newMaxAttachmentsPerDocument = flagMaxAttachmentsPerDocument
			}

			newRemoveOnDetach := project.RemoveOnDetach
			if cmd.Flags().Lookup("remove-on-detach").Changed {
				newRemoveOnDetach = flagRemoveOnDetach
			}

			updatableProjectFields := &types.UpdatableProjectFields{
				Name:                        &newName,
				AuthWebhookURL:              &newAuthWebhookURL,
				AuthWebhookMethods:          &newAuthWebhookMethods,
				AuthWebhookMaxRetries:       &newAuthWebhookMaxRetries,
				AuthWebhookMinWaitInterval:  &newAuthWebhookMinWaitInterval,
				AuthWebhookMaxWaitInterval:  &newAuthWebhookMaxWaitInterval,
				EventWebhookURL:             &newEventWebhookURL,
				EventWebhookEvents:          &newEventWebhookEvents,
				EventWebhookMaxRetries:      &newEventWebhookMaxRetries,
				EventWebhookMinWaitInterval: &newEventWebhookMinWaitInterval,
				EventWebhookMaxWaitInterval: &newEventWebhookMaxWaitInterval,
				ClientDeactivateThreshold:   &newClientDeactivateThreshold,
				MaxSubscribersPerDocument:   &newMaxSubscribersPerDocument,
				MaxAttachmentsPerDocument:   &newMaxAttachmentsPerDocument,
				RemoveOnDetach:              &newRemoveOnDetach,
			}

			updated, err := cli.UpdateProject(ctx, id, updatableProjectFields)
			if err != nil {
				var connErr *connect.Error
				if errors.As(err, &connErr) {
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
	return updated
}

func init() {
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
		0,
		"Maximum number of retries for authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&flagAuthWebhookMinWaitInterval,
		"auth-webhook-min-wait-interval",
		0,
		"Minimum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagAuthWebhookMaxWaitInterval,
		"auth-webhook-max-wait-interval",
		0,
		"Maximum wait interval between retries(exponential backoff).",
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
		0,
		"Maximum number of retries for event webhook.",
	)
	cmd.Flags().DurationVar(
		&flagEventWebhookMinWaitInterval,
		"event-webhook-min-wait-interval",
		0,
		"Minimum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagEventWebhookMaxWaitInterval,
		"event-webhook-max-wait-interval",
		0,
		"Maximum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&flagClientDeactivateThreshold,
		"client-deactivate-threshold",
		0,
		"client deactivate threshold for housekeeping",
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
	SubCmd.AddCommand(cmd)
}
