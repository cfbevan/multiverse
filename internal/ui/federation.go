package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	ap "github.com/cfbevan/multiverse/internal/platform/activitypub"
)

const (
	outboxMarkTimeout   = 2 * time.Second
	maxErrorLogLength   = 512
	defaultOutboxPollS  = 5
	outboxBatchTimeout  = 5 * time.Second
	outboxBatchPageSize = 20
)

func (app *Application) runOutboxWorker(ctx context.Context) {
	pollSeconds := app.config.OutboxPollSec
	if pollSeconds < 1 {
		pollSeconds = defaultOutboxPollS
	}
	ticker := time.NewTicker(time.Duration(pollSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			app.processOutboxBatch(ctx)
		}
	}
}

func (app *Application) processOutboxBatch(ctx context.Context) {
	batchCtx, cancel := context.WithTimeout(ctx, outboxBatchTimeout)
	defer cancel()

	events, err := app.outboxEvents.ClaimDue(batchCtx, time.Now().UTC(), outboxBatchPageSize)
	if err != nil {
		app.logger.Error("outbox claim failed", "error", err.Error())

		return
	}

	for _, event := range events {
		if err := app.processOutboundEvent(ctx, event); err != nil {
			if ctx.Err() != nil {
				return
			}
			app.logger.Error("outbox event failed", "event_id", event.ID, "error", err.Error())
		}
	}
}

func (app *Application) processOutboundEvent(ctx context.Context, event models.OutboxEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := app.deliverOutboxEvent(ctx, event); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		return app.markDeliveryFailure(ctx, event, err)
	}

	return app.markDeliverySuccess(ctx, event)
}

func (app *Application) markDeliveryFailure(
	ctx context.Context,
	event models.OutboxEvent,
	err error,
) error {
	nextAttempt := time.Now().UTC().Add(ap.RetryBackoff(event.Attempts))
	markCtx, markCancel := context.WithTimeout(ctx, outboxMarkTimeout)
	defer markCancel()

	markErr := app.outboxEvents.MarkFailed(
		markCtx,
		event.ID,
		truncateError(err),
		nextAttempt,
	)
	if markErr != nil {
		app.logger.Error(
			"outbox mark failed",
			"event_id",
			event.ID,
			"error",
			markErr.Error(),
		)
	}

	return nil
}

func (app *Application) markDeliverySuccess(ctx context.Context, event models.OutboxEvent) error {
	markCtx, markCancel := context.WithTimeout(ctx, outboxMarkTimeout)
	defer markCancel()

	if err := app.outboxEvents.MarkSent(markCtx, event.ID, time.Now().UTC()); err != nil {
		app.logger.Error("outbox mark sent failed", "event_id", event.ID, "error", err.Error())
	}

	return nil
}

func (app *Application) deliverOutboxEvent(ctx context.Context, event models.OutboxEvent) error {
	actor, err := app.actors.GetByID(ctx, event.ActorID)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		event.TargetInboxURL,
		bytes.NewReader(event.Payload),
	)
	if err != nil {
		return err
	}

	date := time.Now().UTC().Format(time.RFC1123)
	digest := ap.BuildDigestHeader(event.Payload)
	req.Header.Set("Date", date)
	req.Header.Set("Digest", digest)
	req.Header.Set("Content-Type", activityContentType)
	req.Header.Set("Accept", activityContentType)
	req.Header.Set("User-Agent", "multiverse/0.1")
	req.Header.Set("Idempotency-Key", event.IdempotencyKey)

	if err := ap.SignRequest(
		req,
		event.Payload,
		actor.PublicKeyID,
		actor.PrivateKeyPEM,
	); err != nil {
		return err
	}

	resp, err := app.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("remote inbox returned %d", resp.StatusCode)
	}

	return nil
}

func (app *Application) enqueueCreateForFollowers(
	ctx context.Context,
	actor *models.Actor,
	object any,
	activityID string,
) error {
	inboxes, err := app.actors.ListFollowerInboxes(ctx, actor.ID)
	if err != nil {
		return err
	}

	for _, inboxURL := range inboxes {
		targetDomain, err := hostnameFromURL(inboxURL)
		if err != nil {
			continue
		}

		payload, err := json.Marshal(envelope{
			activityContextKey: []string{activityStreamsNS},
			"id":               activityID + "/to/" + targetDomain,
			typeKey:            createActivityType,
			actorKey: actorURL(
				strings.TrimSuffix(app.config.ActivityPubBaseURL, "/"),
				actor.Handle,
			),
			objectKey: object,
		})
		if err != nil {
			return err
		}

		event := &models.OutboxEvent{
			ActorID:        actor.ID,
			TargetInboxURL: inboxURL,
			TargetDomain:   targetDomain,
			IdempotencyKey: fmt.Sprintf("%s-%s", activityID, targetDomain),
			Payload:        payload,
			Status:         "queued",
			Attempts:       0,
			NextAttemptAt:  time.Now().UTC(),
		}
		if err := app.outboxEvents.Enqueue(ctx, event); err != nil {
			app.logger.Error(
				"failed to enqueue outbox event",
				"error",
				err.Error(),
				"target",
				targetDomain,
			)
		}
	}

	return nil
}

func hostnameFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	return u.Hostname(), nil
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxErrorLogLength {
		return s[:maxErrorLogLength]
	}

	return s
}
