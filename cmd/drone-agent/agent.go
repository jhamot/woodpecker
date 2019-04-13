// Copyright 2018 Drone.IO Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"net/http"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/laszlocph/drone-oss-08/cncd/pipeline/pipeline/backend/docker"
	"github.com/laszlocph/drone-oss-08/cncd/pipeline/pipeline/rpc"
	"github.com/laszlocph/drone-oss-08/runner"

	"github.com/drone/signal"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tevino/abool"
	"github.com/urfave/cli"
	oldcontext "golang.org/x/net/context"
)

var counter = &runner.State{
	Metadata: map[string]runner.Info{},
}

func loop(c *cli.Context) error {
	filter := rpc.Filter{
		Labels: map[string]string{
			"platform": c.String("platform"),
		},
		Expr: c.String("filter"),
	}

	hostname := c.String("hostname")
	if len(hostname) == 0 {
		hostname, _ = os.Hostname()
	}

	if c.BoolT("debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	}

	if c.Bool("pretty") {
		log.Logger = log.Output(
			zerolog.ConsoleWriter{
				Out:     os.Stderr,
				NoColor: c.BoolT("nocolor"),
			},
		)
	}

	counter.Polling = c.Int("max-procs")
	counter.Running = 0

	if c.BoolT("healthcheck") {
		go http.ListenAndServe(":3000", nil)
	}

	// TODO pass version information to grpc server
	// TODO authenticate to grpc server

	// grpc.Dial(target, ))

	conn, err := grpc.Dial(
		c.String("server"),
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(&credentials{
			username: c.String("username"),
			password: c.String("password"),
		}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    c.Duration("keepalive-time"),
			Timeout: c.Duration("keepalive-timeout"),
		}),
	)

	if err != nil {
		return err
	}
	defer conn.Close()

	client := rpc.NewGrpcClient(conn)

	sigterm := abool.New()
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("hostname", hostname),
	)
	ctx = signal.WithContextFunc(ctx, func() {
		println("ctrl+c received, terminating process")
		sigterm.Set()
	})

	var wg sync.WaitGroup
	parallel := c.Int("max-procs")
	wg.Add(parallel)

	for i := 0; i < parallel; i++ {
		go func() {
			defer wg.Done()
			for {
				if sigterm.IsSet() {
					return
				}

				engine, err := docker.NewEnv()
				if err != nil {
					log.Error().Err(err).Msg("cannot create docker client")
					return
				}

				r := runner.NewRunner(client, filter, hostname, counter, &engine)
				if err := r.Run(ctx); err != nil {
					log.Error().Err(err).Msg("pipeline done with error")
					return
				}
			}
		}()
	}

	wg.Wait()
	return nil
}

type credentials struct {
	username string
	password string
}

func (c *credentials) GetRequestMetadata(oldcontext.Context, ...string) (map[string]string, error) {
	return map[string]string{
		"username": c.username,
		"password": c.password,
	}, nil
}

func (c *credentials) RequireTransportSecurity() bool {
	return false
}
