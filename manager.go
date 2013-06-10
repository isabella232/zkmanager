package zkmanager

import (
	"github.com/petar/gozk"
	"github.com/skynetservices/skynet2"
	"github.com/skynetservices/skynet2/log"
	"strings"
	"sync"
	"time"
)

/* TODO: Lot's of testing and error handling */
type ZookeeperServiceManager struct {
	conn *zookeeper.Conn
}

type subscriber struct {
	query          skynet.ServiceQuery
	serviceChannel chan skynet.ServiceUpdate
}

var subscribers []subscriber
var servicesLock sync.Mutex

//Changes to these two will require synchronization
var currentServices map[string]*skynet.ServiceInfo
var changedServices []*skynet.ServiceUpdate

func init() {
	subscribers = make([]subscriber, 0)
	currentServices = make(map[string]*skynet.ServiceInfo)
	changedServices = make([]*skynet.ServiceUpdate, 0)
}

func NewZookeeperServiceManager(servers string, timeout time.Duration) skynet.ServiceManager {
	zk, session, err := zookeeper.Dial(servers, timeout)
	if err != nil {
		log.Panic("Couldn't connect: " + err.Error())
	}

	// Wait for connection.
	event := <-session

	if event.State != zookeeper.STATE_CONNECTED {
		log.Panic("Couldn't connect to zookeeper")
	}

	sm := &ZookeeperServiceManager{
		conn: zk,
	}
	//Lock and update the services list
	servicesLock.Lock()
	currentServices = sm.getAllInstances()
	servicesLock.Unlock()
	go watchZookeeper(&ZookeeperServiceManager{
		conn: zk,
	})
	return sm
}

func (sm *ZookeeperServiceManager) getAllInstances() (instanceList map[string]*skynet.ServiceInfo) {
	instances := sm.ListInstances(skynet.ServiceQuery{})
	for _, instance := range instances {
		instanceList[instance.UUID] = &instance
	}
	return
}

func (sm *ZookeeperServiceManager) Subscribe(query skynet.ServiceQuery) chan skynet.ServiceUpdate {
	updateChan := make(chan skynet.ServiceUpdate)
	subscribers = append(subscribers, subscriber{query: query, serviceChannel: updateChan})
	return updateChan
}

func (sm *ZookeeperServiceManager) Add(s skynet.ServiceInfo) {
	log.Println(log.TRACE, "Adding service to cluster", s.ServiceConfig.UUID)

	sm.addService(s)
	sm.createPaths(s)
}

func (sm *ZookeeperServiceManager) Update(s skynet.ServiceInfo) {
	log.Println(log.TRACE, "Updating service", s.ServiceConfig.UUID)
	sm.updateService(s)
}

func (sm *ZookeeperServiceManager) Remove(uuid string) {
	log.Println(log.TRACE, "Removing service", uuid)
}

func (sm *ZookeeperServiceManager) Register(uuid string) {
	log.Println(log.TRACE, "Registering service", uuid)

	sm.conn.Set("/instances/"+uuid+"/registered", "1", -1)
}

func (sm *ZookeeperServiceManager) Unregister(uuid string) {
	log.Println(log.TRACE, "Unregister service", uuid)
	sm.conn.Set("/instances/"+uuid+"/registered", "0", -1)
}

func (sm *ZookeeperServiceManager) ListRegions(query skynet.ServiceQuery) []string {
	d, _, _ := sm.conn.Children("/regions")
	log.Println(log.TRACE, d)
	return d
}

func (sm *ZookeeperServiceManager) ListServices(query skynet.ServiceQuery) []string {
	d, _, _ := sm.conn.Children("/services")
	log.Println(log.TRACE, d)
	return d
}
func (sm *ZookeeperServiceManager) ListInstances(query skynet.ServiceQuery) []skynet.ServiceInfo {
	//TODO do something about that query
	d, _, _ := sm.conn.Children("/instances")
	log.Println(log.TRACE, d)
	r := make([]skynet.ServiceInfo, 0)
	for _, i := range d {
		name, _, _ := sm.conn.Get("/instances/" + i + "/name")
		region, _, _ := sm.conn.Get("/instances/" + i + "/region")
		version, _, _ := sm.conn.Get("/instances/" + i + "/version")
		addr, _, _ := sm.conn.Get("/instances/" + i + "/addr")
		bindaddr, _ := skynet.BindAddrFromString(addr)
		r = append(r, skynet.ServiceInfo{ServiceConfig: &skynet.ServiceConfig{UUID: i, Name: name, Region: region, Version: version, ServiceAddr: bindaddr}})
	}
	return r
}
func (sm *ZookeeperServiceManager) ListHosts(query skynet.ServiceQuery) []string {
	d, _, _ := sm.conn.Children("/hosts")
	log.Println(log.TRACE, d)
	return d
}

func (sm *ZookeeperServiceManager) updateService(s skynet.ServiceInfo) {
	_, err := sm.conn.Set("/instances/"+s.ServiceConfig.UUID+"/addr", s.ServiceConfig.ServiceAddr.String(), -1)
	if err != nil {
		log.Println(log.ERROR, "Updating service", err)
	}
	_, err = sm.conn.Set("/instances/"+s.ServiceConfig.UUID+"/name", s.ServiceConfig.Name, -1)
	if err != nil {
		log.Println(log.ERROR, "Updating service", err)
	}
	_, err = sm.conn.Set("/instances/"+s.ServiceConfig.UUID+"/version", s.ServiceConfig.Version, -1)
	if err != nil {
		log.Println(log.ERROR, "Updating service", err)
	}
	_, err = sm.conn.Set("/instances/"+s.ServiceConfig.UUID+"/region", s.ServiceConfig.Region, -1)
	if err != nil {
		log.Println(log.ERROR, "Updating service", err)
	}
}

func (sm *ZookeeperServiceManager) addService(s skynet.ServiceInfo) {
	sm.createPath("/instances/" + s.ServiceConfig.UUID)
	sm.conn.Create("/instances/"+s.ServiceConfig.UUID+"/registered", "0", zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
	sm.conn.Create("/instances/"+s.ServiceConfig.UUID+"/addr", s.ServiceConfig.ServiceAddr.String(), zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
	sm.conn.Create("/instances/"+s.ServiceConfig.UUID+"/name", s.ServiceConfig.Name, zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
	log.Println(log.ERROR, s.ServiceConfig.Version)
	sm.conn.Create("/instances/"+s.ServiceConfig.UUID+"/version", s.ServiceConfig.Version, zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
	sm.conn.Create("/instances/"+s.ServiceConfig.UUID+"/region", s.ServiceConfig.Region, zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
}

func (sm *ZookeeperServiceManager) createPaths(s skynet.ServiceInfo) {
	// Add UUID to /regions
	sm.createPath("/regions/" + s.ServiceConfig.Region)
	sm.conn.Create("/regions/"+s.ServiceConfig.Region+"/"+s.ServiceConfig.UUID, "", zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))

	// Add UUID to /services/ServiceName and /services/ServiceName/Version
	sm.createPath("/services/" + s.ServiceConfig.Name + "/" + s.ServiceConfig.Version)
	sm.conn.Create("/services/"+s.ServiceConfig.Name+"/"+s.ServiceConfig.UUID, "", zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
	sm.conn.Create("/services/"+s.ServiceConfig.Name+"/"+s.ServiceConfig.Version+"/"+s.ServiceConfig.UUID, "", zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))

	// Add UUID to /hosts/IPAddress
	sm.createPath("/hosts/" + s.ServiceConfig.ServiceAddr.IPAddress)
	sm.conn.Create("/hosts/"+s.ServiceConfig.ServiceAddr.IPAddress+"/"+s.ServiceConfig.UUID, "", zookeeper.EPHEMERAL, zookeeper.WorldACL(zookeeper.PERM_ALL))
}

func (sm *ZookeeperServiceManager) createPath(path string) error {
	parts := strings.Split(path, "/")
	path = ""

	for _, p := range parts {
		if p == "" {
			continue
		}

		path = path + "/" + p

		if stat, _ := sm.conn.Exists(path); stat != nil {
			log.Println(log.DEBUG, "ZK path exists: "+path)
			continue
		}

		log.Println(log.TRACE, "Creating ZK path: "+path)

		_, err := sm.conn.Create(path, "", 0, zookeeper.WorldACL(zookeeper.PERM_ALL))

		if err != nil {
			log.Println(log.ERROR, err.Error())
			return err
		}
	}

	return nil
}

//This function watches Zookeeper for changes to the skynet nodes.
//When a change event is triggered, the change is sent to all subscribers
//based on their subscription query
func watchZookeeper(sm *ZookeeperServiceManager) {

	//reset ChangedServices
	changeServices = changedServices[:]

	//Endless loop watching instances
	for {
		// Create a watch on /instances
		d, _, events, err := sm.conn.ChildrenW("/instances")
		if err != nil {
			panic(err)
		}

		// wait on the event of a change in /instances
		<-events
		// Get a list of current instances
		instances := sm.ListInstances(skynet.ServiceQuery{})

		// Look for additions - items in the instances array not in the currentServices map
		for _, i := range instances {
			s, ok := currentServices[i.UUID]
			if !ok {
				// add it to the currentServices map
				servicesLock.Lock()
				currentServices[i.UUID] = i
				// add it to changedServices
				changedServices = append(changedServices, &skynet.ServiceUpdate{Service: i, Event: skynet.ADD})
				servicesLock.Unlock()
			}
		}

		// Look for subtractions - things in the currentServices map that aren't in the instances list from zk
		for index, inst := range currentServices {
			var found bool
			for l, in := range instances {
				// compare instance "in" to see if it's in the map
				// if not in the map, remove it
				s, ok := currentServices[in.UUID]
				if ok {
					found = true
					break
				}
			}
			if !found {
				// remove from currentServices
				servicesLock.Lock()
				delete(currentServices, inst.UUID)
				changedServices = append(changedServices, &skynet.ServiceUpdate{Service: i, Event: skynet.DELETE})
				servicesLock.Unlock()
			}
		}

		// now we have all additions and deletions in the changedServices slice - send them to subscribers
		// loop through subscribers first
		for _, sub := range subscribers {
			// make a list of notifications for this subscriber
			// use a map so we only update once per query item
			var notifications = make(map[string]skynet.ServiceUpdate)
			// now loop through changes to see if they match a query
			//TODO - rethink query?  this is very boolean/OR oriented and may not be too useful.
			for _, c := range changedServices {
				if sub.query.Name == c.Service.Name {
					notifications[c.Service.UUID] = c
				}
				if sub.query.Region == c.Service.Region {
					notifications[c.Service.UUID] = c
				}
				if sub.query.Version == c.Service.Version {
					notifications[c.Service.UUID] = c
				}
			}
			//now send the notifications
			for x := range notifications {
				sub.serviceChannel <- x
			}
		}
	}
}
