package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
)

type service struct {
	clusterArn *string
	serviceArn *string
}

type task struct {
	service
	taskArn              *string
	containerInstanceArn *string
}

type containerInstance struct {
	task
	ec2InstanceId *string
}

func searchServices(ctx aws.Context, client *ecs.ECS, serviceQuery string) ([]*service, error) {
	var clustersArns []*string
	err := client.ListClustersPagesWithContext(ctx, nil, func(cOut *ecs.ListClustersOutput, lastPage bool) bool {
		clustersArns = append(clustersArns, cOut.ClusterArns...)
		return true
	})
	if err != nil {
		return nil, err
	}

	var services []*service
	for _, clusterArn := range clustersArns {
		err = client.ListServicesPagesWithContext(ctx, &ecs.ListServicesInput{Cluster: clusterArn}, func(sOut *ecs.ListServicesOutput, lastPage bool) bool {
			for _, serviceArn := range sOut.ServiceArns {
				if strings.Contains(*serviceArn, serviceQuery) {
					services = append(services, &service{clusterArn: clusterArn, serviceArn: serviceArn})
				}
			}
			return true
		})
		if err != nil {
			return nil, err
		}
	}

	return services, nil
}

func searchTasks(ctx aws.Context, client *ecs.ECS, services []*service) ([]*task, error) {
	clustersTasks := make(map[string][]*task)
	for _, service := range services {
		err := client.ListTasksPagesWithContext(ctx, &ecs.ListTasksInput{Cluster: service.clusterArn, ServiceName: service.serviceArn}, func(tOut *ecs.ListTasksOutput, lastPage bool) bool {
			for _, taskArn := range tOut.TaskArns {
				t := &task{service: *service, taskArn: taskArn}
				clusterTasks, ok := clustersTasks[*service.clusterArn]
				if ok {
					clustersTasks[*service.clusterArn] = append(clusterTasks, t)
				} else {
					clustersTasks[*service.clusterArn] = []*task{t}
				}
			}

			return true
		})
		if err != nil {
			return nil, err
		}
	}

	var result []*task
	for clusterArn, tasks := range clustersTasks {
		var tasksArn []*string
		taskMap := make(map[string]*task)
		for _, t := range tasks {
			tasksArn = append(tasksArn, t.taskArn)
			taskMap[*t.taskArn] = t
		}

		dOut, err := client.DescribeTasksWithContext(ctx, &ecs.DescribeTasksInput{Cluster: &clusterArn, Tasks: tasksArn})
		if err != nil {
			return nil, err
		}

		for _, ecsTask := range dOut.Tasks {
			oldTask := taskMap[*ecsTask.TaskArn]
			result = append(result, &task{service: oldTask.service, taskArn: oldTask.taskArn, containerInstanceArn: ecsTask.ContainerInstanceArn})
		}
	}

	return result, nil
}

func searchContainerInstances(ctx aws.Context, client *ecs.ECS, tasks []*task) ([]*containerInstance, error) {
	clustersTasks := make(map[string][]*task)
	for _, t := range tasks {
		clusterTasks, ok := clustersTasks[*t.clusterArn]
		if ok {
			clustersTasks[*t.clusterArn] = append(clusterTasks, t)
		} else {
			clustersTasks[*t.clusterArn] = []*task{t}
		}
	}

	var result []*containerInstance
	for clusterArn, clusterTasks := range clustersTasks {
		var containerInstancesArn []*string
		taskMap := make(map[string]*task)
		for _, t := range clusterTasks {
			containerInstancesArn = append(containerInstancesArn, t.containerInstanceArn)
			taskMap[*t.containerInstanceArn] = t
		}

		ciOut, err := client.DescribeContainerInstancesWithContext(ctx, &ecs.DescribeContainerInstancesInput{Cluster: &clusterArn, ContainerInstances: containerInstancesArn})
		if err != nil {
			return nil, err
		}

		for _, ci := range ciOut.ContainerInstances {
			oldTask := taskMap[*ci.ContainerInstanceArn]
			result = append(result, &containerInstance{task: *oldTask, ec2InstanceId: ci.Ec2InstanceId})
		}
	}

	return result, nil
}

func main() {
	var serviceQuery string
	flag.StringVar(&serviceQuery, "query", "", "Query used to filter services")
	flag.Parse()

	if len(serviceQuery) == 0 {
		fmt.Fprintf(os.Stderr, "You must specify a query in order to filter services\nUse --help for more\n\n")
		os.Exit(1)
	}

	s, err := session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating session: %v", err)
		os.Exit(1)
	}

	c := ecs.New(s)
	ctx := aws.BackgroundContext()

	services, err := searchServices(ctx, c, serviceQuery)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}

	tasks, err := searchTasks(ctx, c, services)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}

	containerInstances, err := searchContainerInstances(ctx, c, tasks)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}

	for _, ci := range containerInstances {
		fmt.Printf("%s\n%s\n%s\n%s\n%s\n\n", *ci.clusterArn, *ci.serviceArn, *ci.taskArn, *ci.containerInstanceArn, *ci.ec2InstanceId)
	}
}
