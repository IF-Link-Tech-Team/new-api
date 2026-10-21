package common

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetPageQueryClampsNegativeValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, target := range []string{
		"/list?p=-1&page_size=-1",
		"/list?p=0&ps=-1",
		"/list?p=1&size=-1",
	} {
		request := httptest.NewRequest("GET", target, nil)
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = request

		page := GetPageQuery(context)

		require.Equal(t, 1, page.Page, target)
		require.Equal(t, ItemsPerPage, page.PageSize, target)
	}
}

func TestGetPageQueryCapsLargePageSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("GET", "/list?p=2&page_size=1000", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	page := GetPageQuery(context)

	require.Equal(t, 2, page.Page)
	require.Equal(t, 100, page.PageSize)
}

func TestGetPageQueryCapsLargePageNumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("GET", "/list?p=2147483647&page_size=100", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request

	page := GetPageQuery(context)

	require.Equal(t, maxPageNumber, page.Page)
	require.Equal(t, 100, page.PageSize)
	require.GreaterOrEqual(t, page.GetStartIdx(), 0)
}
