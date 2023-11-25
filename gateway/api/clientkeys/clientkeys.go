package apiclientkeys

// var rfc1035Err = "invalid name. It must contain 63 characters; start and end with alphanumeric lowercase character or contains '-'"

// func isValidRFC1035LabelName(label string) bool {
// 	re := regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)
// 	if len(label) > 63 || !re.MatchString(label) {
// 		return false
// 	}
// 	return true
// }

// type ClientKeysRequest struct {
// 	Name   string `json:"name"`
// 	Active bool   `json:"active"`
// }

// func Post(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	log.Warnf("POST /api/clientkeys is deprecated, user %v must use client keys instead", ctx.UserEmail)
// 	c.JSON(http.StatusGone, gin.H{"message": "endpoint deprecated, use agents instead"})
// }

// func Put(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	var reqBody ClientKeysRequest
// 	if err := c.ShouldBindJSON(&reqBody); err != nil {
// 		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
// 		return
// 	}
// 	clientKeyName := c.Param("name")
// 	clientKey, err := clientkeysstorage.GetByName(ctx, clientKeyName)
// 	if err != nil {
// 		log.Errorf("failed obtaining client key, err=%v", err)
// 		sentry.CaptureException(err)
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 		return
// 	}
// 	if clientKey == nil {
// 		c.JSON(http.StatusNotFound, gin.H{"message": "client key not found"})
// 		return
// 	}
// 	obj, _, err := clientkeysstorage.Put(ctx, clientKeyName, reqBody.Active)
// 	if err != nil {
// 		log.Errorf("failed updating client key, err=%v", err)
// 		sentry.CaptureException(err)
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 		return
// 	}
// 	c.PureJSON(200, obj)
// }

// func List(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	itemList, err := clientkeysstorage.List(ctx)
// 	if err != nil {
// 		log.Errorf("failed listing client keys, err=%v", err)
// 		sentry.CaptureException(err)
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 		return
// 	}
// 	c.PureJSON(200, itemList)
// }

// func Get(c *gin.Context) {
// 	ctx := storagev2.ParseContext(c)
// 	clientKeyName := c.Param("name")
// 	obj, err := clientkeysstorage.GetByName(ctx, clientKeyName)
// 	if err != nil {
// 		log.Errorf("failed obtaining client key, err=%v", err)
// 		sentry.CaptureException(err)
// 		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
// 		return
// 	}
// 	if obj == nil {
// 		c.JSON(http.StatusNotFound, gin.H{"message": "client key not found"})
// 		return
// 	}

// 	c.PureJSON(200, obj)
// }
