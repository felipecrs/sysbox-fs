// Code generated by mockery v1.0.0. DO NOT EDIT.

package mocks

import (
	domain "github.com/nestybox/sysbox-fs/domain"
	mock "github.com/stretchr/testify/mock"
)

// FuseServerServiceIface is an autogenerated mock type for the FuseServerServiceIface type
type FuseServerServiceIface struct {
	mock.Mock
}

// CreateFuseServer provides a mock function with given fields: cntr
func (_m *FuseServerServiceIface) CreateFuseServer(cntr domain.ContainerIface) error {
	ret := _m.Called(cntr)

	var r0 error
	if rf, ok := ret.Get(0).(func(domain.ContainerIface) error); ok {
		r0 = rf(cntr)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// DestroyFuseServer provides a mock function with given fields: mp
func (_m *FuseServerServiceIface) DestroyFuseServer(mp string) error {
	ret := _m.Called(mp)

	var r0 error
	if rf, ok := ret.Get(0).(func(string) error); ok {
		r0 = rf(mp)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// DestroyFuseService provides a mock function with given fields:
func (_m *FuseServerServiceIface) DestroyFuseService() {
	_m.Called()
}

// Setup provides a mock function with given fields: mp, css, ios, hds
func (_m *FuseServerServiceIface) Setup(mp string, css domain.ContainerStateServiceIface, ios domain.IOServiceIface, hds domain.HandlerServiceIface) {
	_m.Called(mp, css, ios, hds)
}
