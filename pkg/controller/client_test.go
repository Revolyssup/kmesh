/*
 * Copyright 2024 The Kmesh Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at:
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package controller

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gotest.tools/assert"

	"kmesh.net/kmesh/pkg/bpf"
	"kmesh.net/kmesh/pkg/constants"
	"kmesh.net/kmesh/pkg/controller/envoy"
	"kmesh.net/kmesh/pkg/controller/workload"
	"kmesh.net/kmesh/pkg/controller/xdstest"
	"kmesh.net/kmesh/pkg/nets"
)

func TestRecoverConnection(t *testing.T) {
	t.Run("test reconnect success", func(t *testing.T) {
		utClient := NewXdsClient()
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		iteration := 0
		netPatches := gomonkey.NewPatches()
		defer netPatches.Reset()
		netPatches.ApplyFunc(nets.GrpcConnect, func(addr string) (*grpc.ClientConn, error) {
			// // more than 2 link failures will result in a long test time
			if iteration < 2 {
				iteration++
				return nil, errors.New("failed to create grpc connect")
			} else {
				// returns a fake grpc connection
				mockDiscovery := xdstest.NewMockServer(t)
				return grpc.Dial("buffcon",
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithBlock(),
					grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
						return mockDiscovery.Listener.Dial()
					}))
			}
		})
		err := utClient.recoverConnection()
		assert.NilError(t, err)
		assert.Equal(t, 2, iteration)
	})
}

func TestClientResponseProcess(t *testing.T) {
	utConfig := bpf.GetConfig()
	utConfig.Mode = constants.AdsMode
	bpfConfig = utConfig
	t.Run("ads stream process failed, test reconnect", func(t *testing.T) {
		netPatches := gomonkey.NewPatches()
		defer netPatches.Reset()
		netPatches.ApplyFunc(nets.GrpcConnect, func(addr string) (*grpc.ClientConn, error) {
			mockDiscovery := xdstest.NewMockServer(t)
			return grpc.Dial("buffcon",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return mockDiscovery.Listener.Dial()
				}))
		})

		utClient := NewXdsClient()
		err := utClient.createGrpcStreamClient()
		assert.NilError(t, err)

		reConnectPatches := gomonkey.NewPatches()
		defer reConnectPatches.Reset()
		iteration := 0
		reConnectPatches.ApplyPrivateMethod(reflect.TypeOf(utClient), "createGrpcStreamClient",
			func(_ *XdsClient) error {
				// more than 2 link failures will result in a long test time
				if iteration < 2 {
					iteration++
					return errors.New("cant connect to client")
				} else {
					return nil
				}
			})
		streamPatches := gomonkey.NewPatches()
		defer streamPatches.Reset()
		streamPatches.ApplyMethod(reflect.TypeOf(utClient.AdsStream), "HandleAdsStream",
			func(_ *envoy.AdsStream) error {
				// if the number of loops is less than or equal to two, an error is reported and a retry is triggered.
				if iteration < 2 {
					return errors.New("stream recv failed")
				} else {
					// it's been cycled more than twice, use context.cancel() to end the current grpc connection.
					utClient.cancel()
					return nil
				}
			})
		utClient.handleUpstream(utClient.ctx)
		assert.Equal(t, 2, iteration)
	})

	t.Run("workload stream process failed, test reconnect", func(t *testing.T) {
		utConfig.Mode = constants.WorkloadMode
		netPatches := gomonkey.NewPatches()
		defer netPatches.Reset()
		netPatches.ApplyFunc(nets.GrpcConnect, func(addr string) (*grpc.ClientConn, error) {
			mockDiscovery := xdstest.NewMockServer(t)
			return grpc.Dial("buffcon",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return mockDiscovery.Listener.Dial()
				}))
		})

		utClient := NewXdsClient()
		err := utClient.createGrpcStreamClient()
		assert.NilError(t, err)

		reConnectPatches := gomonkey.NewPatches()
		defer reConnectPatches.Reset()
		iteration := 0
		reConnectPatches.ApplyPrivateMethod(reflect.TypeOf(utClient), "createGrpcStreamClient",
			func(_ *XdsClient) error {
				// more than 2 link failures will result in a long test time
				if iteration < 2 {
					iteration++
					return errors.New("cant connect to client")
				} else {
					return nil
				}
			})
		streamPatches := gomonkey.NewPatches()
		defer streamPatches.Reset()
		streamPatches.ApplyMethod(reflect.TypeOf(utClient.workloadStream), "HandleWorkloadStream",
			func(_ *workload.WorkloadStream) error {
				if iteration < 2 {
					return errors.New("stream recv failed")
				} else {
					utClient.cancel()
					return nil
				}
			})
		utClient.handleUpstream(utClient.ctx)
		assert.Equal(t, 2, iteration)
	})
}
