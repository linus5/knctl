/*
Copyright 2018 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package route

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cppforlife/go-cli-ui/ui"
	cmdcore "github.com/cppforlife/knctl/pkg/knctl/cmd/core"
	cmdflags "github.com/cppforlife/knctl/pkg/knctl/cmd/flags"
	cmdrev "github.com/cppforlife/knctl/pkg/knctl/cmd/revision"
	ctlservice "github.com/cppforlife/knctl/pkg/knctl/service"
	"github.com/cppforlife/knctl/pkg/knctl/util"
	"github.com/knative/serving/pkg/apis/serving/v1alpha1"
	servingclientset "github.com/knative/serving/pkg/client/clientset/versioned"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CreateOptions struct {
	ui          ui.UI
	depsFactory cmdcore.DepsFactory

	RouteFlags   RouteFlags
	TrafficFlags TrafficFlags
}

func NewCreateOptions(ui ui.UI, depsFactory cmdcore.DepsFactory) *CreateOptions {
	return &CreateOptions{ui: ui, depsFactory: depsFactory}
}

func NewCreateCmd(o *CreateOptions, flagsFactory cmdcore.FlagsFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create or update route",
		Example: `
  # Set traffic percentages for service 'svc1' in namespace 'ns1'
  knctl route create --route rt1 -p svc1:latest=20% -p svc1:previous=80% -n ns1

  # Roll back traffic for previous revision of service 'svc1' in namespace 'ns1'
  knctl route create --route rt1 -p svc1:previous=100% -n ns1`,
		RunE: func(_ *cobra.Command, _ []string) error { return o.Run() },
	}
	o.RouteFlags.Set(cmd, flagsFactory)
	o.TrafficFlags.Set(cmd, flagsFactory)
	return cmd
}

func (o *CreateOptions) Run() error {
	servingClient, err := o.depsFactory.ServingClient()
	if err != nil {
		return err
	}

	tags := ctlservice.NewTags(servingClient)

	route := &v1alpha1.Route{
		ObjectMeta: o.TrafficFlags.GenerateNameFlags.Apply(metav1.ObjectMeta{
			Name:      o.RouteFlags.Name,
			Namespace: o.RouteFlags.NamespaceFlags.Name,
		}),
	}

	var targets []v1alpha1.TrafficTarget

	for _, traffic := range o.TrafficFlags.Percentages {
		pieces := strings.SplitN(traffic, "=", 2)
		if len(pieces) != 2 {
			return fmt.Errorf("Expected percentage to be in format 'revision=percentage'")
		}

		percent, err := strconv.Atoi(strings.TrimSuffix(pieces[1], "%"))
		if err != nil {
			return fmt.Errorf("Expected percentage value to be an integer")
		}

		if percent < 0 || percent > 100 {
			return fmt.Errorf("Expected percentage value to be between 0%% and 100%%")
		}

		revFlags := cmdflags.RevisionFlags{Name: pieces[0], NamespaceFlags: o.RouteFlags.NamespaceFlags}

		revision, err := cmdrev.NewReference(revFlags, tags, servingClient).Revision()
		if err != nil {
			return err
		}

		targets = append(targets, v1alpha1.TrafficTarget{
			RevisionName: revision.Name,
			Percent:      percent,
			// TODO ConfiguratioName 'service:'?
			// TODO Name
		})
	}

	route.Spec.Traffic = targets

	return o.createOrUpdate(servingClient, route)
}

func (o *CreateOptions) createOrUpdate(servingClient servingclientset.Interface, route *v1alpha1.Route) error {
	_, createErr := servingClient.ServingV1alpha1().Routes(o.RouteFlags.NamespaceFlags.Name).Create(route)
	if createErr != nil {
		if errors.IsAlreadyExists(createErr) {
			return o.update(servingClient, route)
		}

		return fmt.Errorf("Creating route: %s", createErr)
	}

	return nil
}

func (o *CreateOptions) update(servingClient servingclientset.Interface, route *v1alpha1.Route) error {
	return util.Retry(time.Second, 10*time.Second, func() (bool, error) {
		origRoute, err := servingClient.ServingV1alpha1().Routes(o.RouteFlags.NamespaceFlags.Name).Get(o.RouteFlags.Name, metav1.GetOptions{})
		if err != nil {
			return true, err
		}

		origRoute.Spec = route.Spec

		_, err = servingClient.ServingV1alpha1().Routes(o.RouteFlags.NamespaceFlags.Name).Update(origRoute)
		if err != nil {
			return false, fmt.Errorf("Updating route: %s", err)
		}

		return true, nil
	})
}
