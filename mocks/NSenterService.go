// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import domain "github.com/nestybox/sysvisor/sysvisor-fs/domain"
import mock "github.com/stretchr/testify/mock"

// NSenterService is an autogenerated mock type for the NSenterService type
type NSenterService struct {
	mock.Mock
}

// LaunchEvent provides a mock function with given fields: e
func (_m *NSenterService) LaunchEvent(e domain.NSenterEventIface) error {
	ret := _m.Called(e)

	var r0 error
	if rf, ok := ret.Get(0).(func(domain.NSenterEventIface) error); ok {
		r0 = rf(e)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewEvent provides a mock function with given fields: path, pid, ns, req, res
func (_m *NSenterService) NewEvent(path string, pid uint32, ns []string, req *domain.NSenterMessage, res *domain.NSenterMessage) domain.NSenterEventIface {
	ret := _m.Called(path, pid, ns, req, res)

	var r0 domain.NSenterEventIface
	if rf, ok := ret.Get(0).(func(string, uint32, []string, *domain.NSenterMessage, *domain.NSenterMessage) domain.NSenterEventIface); ok {
		r0 = rf(path, pid, ns, req, res)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(domain.NSenterEventIface)
		}
	}

	return r0
}

// ResponseEvent provides a mock function with given fields: e
func (_m *NSenterService) ResponseEvent(e domain.NSenterEventIface) *domain.NSenterMessage {
	ret := _m.Called(e)

	var r0 *domain.NSenterMessage
	if rf, ok := ret.Get(0).(func(domain.NSenterEventIface) *domain.NSenterMessage); ok {
		r0 = rf(e)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*domain.NSenterMessage)
		}
	}

	return r0
}
