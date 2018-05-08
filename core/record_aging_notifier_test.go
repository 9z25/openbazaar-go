package core

import (
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/OpenBazaar/jsonpb"
	"github.com/OpenBazaar/openbazaar-go/pb"
	"github.com/OpenBazaar/openbazaar-go/repo"
	"github.com/OpenBazaar/openbazaar-go/repo/db"
	"github.com/OpenBazaar/openbazaar-go/schema"
	"github.com/OpenBazaar/openbazaar-go/test/factory"
	"github.com/op/go-logging"
)

func TestPerformTaskCreatesModeratorDisputeExpiryNotifications(t *testing.T) {
	// Start each case 50 days ago and have the lastNotifiedAt at a day after
	// each notification is suppose to be sent. With no notifications already queued,
	// it should produce all the old notifications up to the most recent one expected
	var (
		broadcastChannel = make(chan repo.Notifier, 0)
		timeStart        = time.Now().Add(time.Duration(-50*24) * time.Hour)
		twelveHours      = time.Duration(12) * time.Hour
		fifteenDays      = time.Duration(15*24) * time.Hour
		fourtyDays       = time.Duration(40*24) * time.Hour
		fourtyFourDays   = time.Duration(44*24) * time.Hour
		fourtyFiveDays   = time.Duration(45*24) * time.Hour

		// Produces notification for 15, 40, 44 and 45 days
		neverNotified = &repo.DisputeCaseRecord{
			CaseID:         "neverNotified",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(twelveHours),
			BuyerContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{Tiny: "never-buyer-tinyimagehash", Small: "never-buyer-smallimagehash"}}}},
				},
			},
			VendorContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{}}}},
				},
			},
			IsBuyerInitiated: true,
		}
		// Produces notification for 40, 44 and 45 days
		notifiedUpToFifteenDay = &repo.DisputeCaseRecord{
			CaseID:         "notifiedUpToFifteenDay",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fifteenDays + twelveHours),
			BuyerContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{}}}},
				},
			},
			VendorContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{Tiny: "fifteen-vendor-tinyimagehash", Small: "fifteen-vendor-smallimagehash"}}}},
				},
			},
			IsBuyerInitiated: false,
		}
		// Produces notification for 44 and 45 days
		notifiedUpToFourtyDays = &repo.DisputeCaseRecord{
			CaseID:         "notifiedUpToFourtyDay",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyDays + twelveHours),
			BuyerContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{Tiny: "fourty-buyer-tinyimagehash", Small: "fourty-buyer-smallimagehash"}}}},
				},
			},
			VendorContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{}}}},
				},
			},
			IsBuyerInitiated: true,
		}
		// Produces notification for 45 days
		notifiedUpToFourtyFourDays = &repo.DisputeCaseRecord{
			CaseID:         "notifiedUpToFourtyFourDays",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyFourDays + twelveHours),
			BuyerContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{}}}},
				},
			},
			VendorContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{Tiny: "fourtyfour-vendor-tinyimagehash", Small: "fourtyfour-vendor-smallimagehash"}}}},
				},
			},
			IsBuyerInitiated: false,
		}
		// Produces no notifications as all have already been created
		notifiedUpToFourtyFiveDays = &repo.DisputeCaseRecord{
			CaseID:         "notifiedUpToFourtyFiveDays",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyFiveDays + twelveHours),
			BuyerContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{}}}},
				},
			},
			VendorContract: &pb.RicardianContract{
				VendorListings: []*pb.Listing{
					{Item: &pb.Listing_Item{Images: []*pb.Listing_Item_Image{{}}}},
				},
			},
			IsBuyerInitiated: false,
		}
		existingRecords = []*repo.DisputeCaseRecord{
			neverNotified,
			notifiedUpToFifteenDay,
			notifiedUpToFourtyDays,
			notifiedUpToFourtyFourDays,
			notifiedUpToFourtyFiveDays,
		}

		appSchema = schema.MustNewCustomSchemaManager(schema.SchemaContext{
			DataPath:        schema.GenerateTempPath(),
			TestModeEnabled: true,
		})
	)

	if err := appSchema.BuildSchemaDirectories(); err != nil {
		t.Fatal(err)
	}
	defer appSchema.DestroySchemaDirectories()
	if err := appSchema.InitializeDatabase(); err != nil {
		t.Fatal(err)
	}

	database, err := appSchema.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}
	s, err := database.Prepare("insert into cases (caseID, buyerContract, vendorContract, timestamp, buyerOpened, lastNotifiedAt) values (?, ?, ?, ?, ?, ?)")
	if err != nil {
		t.Fatal(err)
	}

	m := jsonpb.Marshaler{
		EnumsAsInts:  false,
		EmitDefaults: true,
		Indent:       "    ",
		OrigName:     false,
	}
	for _, r := range existingRecords {
		var isBuyerInitiated int = 0
		if r.IsBuyerInitiated {
			isBuyerInitiated = 1
		}
		buyerContractData, err := m.MarshalToString(r.BuyerContract)
		if err != nil {
			t.Fatal(err)
		}
		vendorContractData, err := m.MarshalToString(r.VendorContract)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Exec(r.CaseID, buyerContractData, vendorContractData, int(r.Timestamp.Unix()), isBuyerInitiated, int(r.LastNotifiedAt.Unix()))
		if err != nil {
			t.Fatal(err)
		}
	}

	var (
		closeAsyncChannelVerifier = make(chan bool, 0)
		broadcastCount            = 0
	)
	go func() {
		for {
			select {
			case n := <-broadcastChannel:
				notifier, ok := n.(repo.Notifier)
				if !ok {
					t.Errorf("unable to cast as Notifier: %+v", n)
				}
				t.Logf("notification received: %s", notifier)
				broadcastCount += 1
			case <-closeAsyncChannelVerifier:
				return
			}
		}
	}()

	datastore := db.NewSQLiteDatastore(database, new(sync.Mutex))
	worker := &recordAgingNotifier{
		datastore: datastore,
		broadcast: broadcastChannel,
		logger:    logging.MustGetLogger("testRecordAgingNotifier"),
	}

	worker.PerformTask()

	// Verify Notifications received in channel
	closeAsyncChannelVerifier <- true
	if broadcastCount != 10 {
		t.Error("Expected 10 notifications to be broadcast, found", broadcastCount)
	}

	// Verify NotificationRecords in datastore
	rows, err := database.Query("select caseID, lastNotifiedAt from cases")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var (
			caseID         string
			lastNotifiedAt int64
		)
		if err := rows.Scan(&caseID, &lastNotifiedAt); err != nil {
			t.Fatal(err)
		}
		switch caseID {
		case neverNotified.CaseID, notifiedUpToFifteenDay.CaseID, notifiedUpToFourtyDays.CaseID, notifiedUpToFourtyFourDays.CaseID:
			durationFromActual := time.Now().Sub(time.Unix(lastNotifiedAt, 0))
			if durationFromActual > (time.Duration(5) * time.Second) {
				t.Errorf("Expected %s to have lastNotifiedAt set when executed, was %s", caseID, time.Unix(lastNotifiedAt, 0).String())
			}
		case notifiedUpToFourtyFiveDays.CaseID:
			if lastNotifiedAt != notifiedUpToFourtyFiveDays.LastNotifiedAt.Unix() {
				t.Error("Expected notifiedUpToFourtyFiveDays to not update LastNotifiedAt")
			}
		default:
			t.Error("Unexpected dispute case")
		}
	}

	actualNotifications, count, err := datastore.Notifications().GetAll("", -1, []string{})
	if err != nil {
		t.Fatal(err)
	}

	if count != 10 {
		t.Errorf("Expected 10 notifications to be produced, but found %d", count)
	}

	var (
		checkNeverNotified_FifteenDay               bool
		checkNeverNotified_FourtyDay                bool
		checkNeverNotified_FourtyFourDay            bool
		checkNeverNotified_FourtyFiveDay            bool
		checkNotifiedToFifteenDays_FourtyDay        bool
		checkNotifiedToFifteenDays_FourtyFourDay    bool
		checkNotifiedToFifteenDays_FourtyFiveDay    bool
		checkNotifiedToFourtyDays_FourtyFourDay     bool
		checkNotifiedToFourtyDays_FourtyFiveDay     bool
		checkNotifiedToFourtyFourDays_FourtyFiveDay bool

		firstInterval_ExpectedExpiresIn  = uint((repo.ModeratorDisputeExpiry_lastInterval - repo.ModeratorDisputeExpiry_firstInterval).Seconds())
		secondInterval_ExpectedExpiresIn = uint((repo.ModeratorDisputeExpiry_lastInterval - repo.ModeratorDisputeExpiry_secondInterval).Seconds())
		thirdInterval_ExpectedExpiresIn  = uint((repo.ModeratorDisputeExpiry_lastInterval - repo.ModeratorDisputeExpiry_thirdInterval).Seconds())
		lastInterval_ExpectedExpiresIn   = uint(0)
	)

	for _, n := range actualNotifications {
		var (
			contract  = &pb.RicardianContract{}
			thumbnail = n.NotifierData.(repo.ModeratorDisputeExpiry).Thumbnail
			refID     = n.NotifierData.(repo.ModeratorDisputeExpiry).CaseID
			expiresIn = n.NotifierData.(repo.ModeratorDisputeExpiry).ExpiresIn
		)
		if refID == neverNotified.CaseID {
			if neverNotified.IsBuyerInitiated {
				contract = neverNotified.BuyerContract
			} else {
				contract = neverNotified.VendorContract
			}
			assertThumbnailValuesAreSet(t, thumbnail, contract)
			if expiresIn == firstInterval_ExpectedExpiresIn {
				checkNeverNotified_FifteenDay = true
				continue
			}
			if expiresIn == secondInterval_ExpectedExpiresIn {
				checkNeverNotified_FourtyDay = true
				continue
			}
			if expiresIn == thirdInterval_ExpectedExpiresIn {
				checkNeverNotified_FourtyFourDay = true
				continue
			}
			if expiresIn == lastInterval_ExpectedExpiresIn {
				checkNeverNotified_FourtyFiveDay = true
				continue
			}
		}
		if refID == notifiedUpToFifteenDay.CaseID {
			if notifiedUpToFifteenDay.IsBuyerInitiated {
				contract = notifiedUpToFifteenDay.BuyerContract
			} else {
				contract = notifiedUpToFifteenDay.VendorContract
			}
			assertThumbnailValuesAreSet(t, thumbnail, contract)
			if expiresIn == secondInterval_ExpectedExpiresIn {
				checkNotifiedToFifteenDays_FourtyDay = true
				continue
			}
			if expiresIn == thirdInterval_ExpectedExpiresIn {
				checkNotifiedToFifteenDays_FourtyFourDay = true
				continue
			}
			if expiresIn == lastInterval_ExpectedExpiresIn {
				checkNotifiedToFifteenDays_FourtyFiveDay = true
				continue
			}
		}
		if refID == notifiedUpToFourtyDays.CaseID {
			if notifiedUpToFourtyDays.IsBuyerInitiated {
				contract = notifiedUpToFourtyDays.BuyerContract
			} else {
				contract = notifiedUpToFourtyDays.VendorContract
			}
			assertThumbnailValuesAreSet(t, thumbnail, contract)
			if expiresIn == thirdInterval_ExpectedExpiresIn {
				checkNotifiedToFourtyDays_FourtyFourDay = true
				continue
			}
			if expiresIn == lastInterval_ExpectedExpiresIn {
				checkNotifiedToFourtyDays_FourtyFiveDay = true
				continue
			}
		}
		if refID == notifiedUpToFourtyFourDays.CaseID && expiresIn == lastInterval_ExpectedExpiresIn {
			if notifiedUpToFourtyFourDays.IsBuyerInitiated {
				contract = notifiedUpToFourtyFourDays.BuyerContract
			} else {
				contract = notifiedUpToFourtyFourDays.VendorContract
			}
			assertThumbnailValuesAreSet(t, thumbnail, contract)
			checkNotifiedToFourtyFourDays_FourtyFiveDay = true
		}
	}

	if checkNeverNotified_FifteenDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNeverNotified_FifteenDay")
	}
	if checkNeverNotified_FourtyDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNeverNotified_FourtyDay")
	}
	if checkNeverNotified_FourtyFourDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNeverNotified_FourtyFourDay")
	}
	if checkNeverNotified_FourtyFiveDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNeverNotified_FourtyFiveDay")
	}
	if checkNotifiedToFifteenDays_FourtyDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNotifiedToFifteenDays_FourtyDay")
	}
	if checkNotifiedToFifteenDays_FourtyFourDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNotifiedToFifteenDays_FourtyFourDay")
	}
	if checkNotifiedToFifteenDays_FourtyFiveDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNotifiedToFifteenDays_FourtyFiveDay")
	}
	if checkNotifiedToFourtyDays_FourtyFourDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNotifiedToFourtyDays_FourtyFourDay")
	}
	if checkNotifiedToFourtyDays_FourtyFiveDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNotifiedToFourtyDays_FourtyFiveDay")
	}
	if checkNotifiedToFourtyFourDays_FourtyFiveDay != true {
		t.Errorf("Expected dispute expiry notification missing: checkNotifiedToFourtyFourDays_FourtyFiveDay")
	}
}

func TestPerformTaskCreatesBuyerDisputeTimeoutNotifications(t *testing.T) {
	// Start each purchase 50 days ago and have the lastNotifiedAt at a day after
	// each notification is suppose to be sent. With no notifications already queued,
	// it should produce all the old notifications up to the most recent one expected
	var (
		broadcastChannel = make(chan repo.Notifier, 0)
		timeStart        = time.Now().Add(time.Duration(-50*24) * time.Hour)
		twelveHours      = time.Duration(12) * time.Hour
		fifteenDays      = time.Duration(15*24) * time.Hour
		fourtyDays       = time.Duration(40*24) * time.Hour
		fourtyFourDays   = time.Duration(44*24) * time.Hour
		fourtyFiveDays   = time.Duration(45*24) * time.Hour

		// Produces notification for 15, 40, 44 and 45 days
		neverNotified = &repo.PurchaseRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "neverNotified",
			Timestamp:      timeStart,
			LastNotifiedAt: time.Unix(0, 0),
		}
		// Produces notification for 40, 44 and 45 days
		notifiedUpToFifteenDay = &repo.PurchaseRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "notifiedUpToFifteenDay",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fifteenDays + twelveHours),
		}
		// Produces notification for 44 and 45 days
		notifiedUpToFourtyDay = &repo.PurchaseRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "notifiedUpToFourtyDay",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyDays + twelveHours),
		}
		// Produces notification for 45 days
		notifiedUpToFourtyFourDays = &repo.PurchaseRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "notifiedUpToFourtyFourDays",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyFourDays + twelveHours),
		}
		// Produces no notifications as all have already been created
		notifiedUpToFourtyFiveDays = &repo.PurchaseRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "notifiedUpToFourtyFiveDays",
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyFiveDays + twelveHours),
		}
		existingRecords = []*repo.PurchaseRecord{
			neverNotified,
			notifiedUpToFifteenDay,
			notifiedUpToFourtyDay,
			notifiedUpToFourtyFourDays,
			notifiedUpToFourtyFiveDays,
		}

		appSchema = schema.MustNewCustomSchemaManager(schema.SchemaContext{
			DataPath:        schema.GenerateTempPath(),
			TestModeEnabled: true,
		})
	)
	neverNotified.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "never-tinyimagehashOne", Small: "never-smallimagehashOne"}}
	notifiedUpToFifteenDay.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "fifteen-tinyimagehashOne", Small: "fifteen-smallimagehashOne"}}
	notifiedUpToFourtyDay.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "fourty-tinyimagehashOne", Small: "fourty-smallimagehashOne"}}
	notifiedUpToFourtyFourDays.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "fourtyfour-tinyimagehashOne", Small: "fourtyfour-smallimagehashOne"}}
	notifiedUpToFourtyFiveDays.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "fourtyfive-tinyimagehashOne", Small: "fourtyfive-smallimagehashOne"}}

	if err := appSchema.BuildSchemaDirectories(); err != nil {
		t.Fatal(err)
	}
	defer appSchema.DestroySchemaDirectories()
	if err := appSchema.InitializeDatabase(); err != nil {
		t.Fatal(err)
	}
	database, err := appSchema.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}

	m := jsonpb.Marshaler{
		EnumsAsInts:  false,
		EmitDefaults: true,
		Indent:       "    ",
		OrigName:     false,
	}
	for _, r := range existingRecords {
		contractData, err := m.MarshalToString(r.Contract)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.Exec("insert into purchases (orderID, contract, state, timestamp, lastNotifiedAt) values (?, ?, ?, ?, ?)", r.OrderID, contractData, int(r.OrderState), int(r.Timestamp.Unix()), int(r.LastNotifiedAt.Unix()))
		if err != nil {
			t.Fatal(err)
		}
	}

	var (
		closeAsyncChannelVerifier = make(chan bool, 0)
		broadcastCount            = 0
	)
	go func() {
		for {
			select {
			case n := <-broadcastChannel:
				notifier, ok := n.(repo.Notifier)
				if !ok {
					t.Errorf("unable to cast as Notifier: %+v", n)
				}
				t.Logf("notification received: %s", notifier.GetType())
				broadcastCount += 1
			case <-closeAsyncChannelVerifier:
				return
			}
		}
	}()

	datastore := db.NewSQLiteDatastore(database, new(sync.Mutex))
	worker := &recordAgingNotifier{
		datastore: datastore,
		broadcast: broadcastChannel,
		logger:    logging.MustGetLogger("testRecordAgingNotifier"),
	}

	worker.PerformTask()

	// Verify Notifications received in channel
	closeAsyncChannelVerifier <- true
	if broadcastCount != 10 {
		t.Error("Expected 10 notifications to be broadcast, found", broadcastCount)
	}

	// Verify NotificationRecords in datastore
	rows, err := database.Query("select orderID, lastNotifiedAt from purchases")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var (
			orderID        string
			lastNotifiedAt int64
		)
		if err := rows.Scan(&orderID, &lastNotifiedAt); err != nil {
			t.Fatal(err)
		}
		switch orderID {
		case neverNotified.OrderID, notifiedUpToFifteenDay.OrderID, notifiedUpToFourtyDay.OrderID, notifiedUpToFourtyFourDays.OrderID:
			durationFromActual := time.Now().Sub(time.Unix(lastNotifiedAt, 0))
			if durationFromActual > (time.Duration(5) * time.Second) {
				t.Errorf("Expected %s to have lastNotifiedAt set when executed, was %s", orderID, time.Unix(lastNotifiedAt, 0).String())
			}
		case notifiedUpToFourtyFiveDays.OrderID:
			if lastNotifiedAt != notifiedUpToFourtyFiveDays.LastNotifiedAt.Unix() {
				t.Error("Expected notifiedUpToFourtyFiveDays to not update LastNotifiedAt")
			}
		default:
			t.Error("Unexpected dispute case")
		}
	}

	var count int64
	err = database.QueryRow("select count(*) from notifications").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Errorf("Expected 10 notifications to be produced, but found %d", count)
	}

	rows, err = database.Query("select notifID, serializedNotification, timestamp from notifications")
	if err != nil {
		t.Fatal(err)
	}

	var (
		checkNeverNotifiedPurchase_FirstNotificationSeen  bool
		checkNeverNotifiedPurchase_SecondNotificationSeen bool
		checkNeverNotifiedPurchase_ThirdNotificationSeen  bool
		checkNeverNotifiedPurchase_LastNotificationSeen   bool
		checkFifteenDayPurchase_SecondNotificationSeen    bool
		checkFifteenDayPurchase_ThirdNotificationSeen     bool
		checkFifteenDayPurchase_LastNotificationSeen      bool
		checkFourtyDayPurchase_ThirdNotificationSeen      bool
		checkFourtyDayPurchase_LastNotificationSeen       bool
		checkFourtyFourDayPurchase_LastNotificationSeen   bool

		firstInterval_ExpectedExpiresIn  = uint((repo.BuyerDisputeTimeout_lastInterval - repo.BuyerDisputeTimeout_firstInterval).Seconds())
		secondInterval_ExpectedExpiresIn = uint((repo.BuyerDisputeTimeout_lastInterval - repo.BuyerDisputeTimeout_secondInterval).Seconds())
		thirdInterval_ExpectedExpiresIn  = uint((repo.BuyerDisputeTimeout_lastInterval - repo.BuyerDisputeTimeout_thirdInterval).Seconds())
		lastInterval_ExpectedExpiresIn   = uint(0)
	)
	for rows.Next() {
		var (
			nID, nJSON string
			nTimestamp sql.NullInt64
			n          *repo.Notification
		)
		if err = rows.Scan(&nID, &nJSON, &nTimestamp); err != nil {
			t.Error(err)
			continue
		}
		if err := json.Unmarshal([]byte(nJSON), &n); err != nil {
			t.Error("Failed unmarshalling notification:", err.Error())
			continue
		}
		var (
			refID     = n.NotifierData.(repo.BuyerDisputeTimeout).OrderID
			expiresIn = n.NotifierData.(repo.BuyerDisputeTimeout).ExpiresIn
			thumbnail = n.NotifierData.(repo.BuyerDisputeTimeout).Thumbnail
		)
		if refID == neverNotified.OrderID {
			assertThumbnailValuesAreSet(t, thumbnail, neverNotified.Contract)
			if expiresIn == firstInterval_ExpectedExpiresIn {
				checkNeverNotifiedPurchase_FirstNotificationSeen = true
				continue
			}
			if expiresIn == secondInterval_ExpectedExpiresIn {
				checkNeverNotifiedPurchase_SecondNotificationSeen = true
				continue
			}
			if expiresIn == thirdInterval_ExpectedExpiresIn {
				checkNeverNotifiedPurchase_ThirdNotificationSeen = true
				continue
			}
			if expiresIn == lastInterval_ExpectedExpiresIn {
				checkNeverNotifiedPurchase_LastNotificationSeen = true
				continue
			}
		}
		if refID == notifiedUpToFifteenDay.OrderID {
			assertThumbnailValuesAreSet(t, thumbnail, notifiedUpToFifteenDay.Contract)
			if expiresIn == secondInterval_ExpectedExpiresIn {
				checkFifteenDayPurchase_SecondNotificationSeen = true
				continue
			}
			if expiresIn == thirdInterval_ExpectedExpiresIn {
				checkFifteenDayPurchase_ThirdNotificationSeen = true
				continue
			}
			if expiresIn == lastInterval_ExpectedExpiresIn {
				checkFifteenDayPurchase_LastNotificationSeen = true
				continue
			}
		}
		if refID == notifiedUpToFourtyDay.OrderID {
			assertThumbnailValuesAreSet(t, thumbnail, notifiedUpToFourtyDay.Contract)
			if expiresIn == thirdInterval_ExpectedExpiresIn {
				checkFourtyDayPurchase_ThirdNotificationSeen = true
				continue
			}
			if expiresIn == lastInterval_ExpectedExpiresIn {
				checkFourtyDayPurchase_LastNotificationSeen = true
				continue
			}
		}
		if refID == notifiedUpToFourtyFourDays.OrderID && expiresIn == lastInterval_ExpectedExpiresIn {
			assertThumbnailValuesAreSet(t, thumbnail, notifiedUpToFourtyFourDays.Contract)
			checkFourtyFourDayPurchase_LastNotificationSeen = true
		}
	}

	if checkNeverNotifiedPurchase_FirstNotificationSeen != true {
		t.Errorf("Expected notification missing: checkNeverNotifiedPurchase_FirstNotificationSeen")
	}
	if checkNeverNotifiedPurchase_SecondNotificationSeen != true {
		t.Errorf("Expected notification missing: checkNeverNotifiedPurchase_SecondNotificationSeen")
	}
	if checkNeverNotifiedPurchase_ThirdNotificationSeen != true {
		t.Errorf("Expected notification missing: checkNeverNotifiedPurchase_ThirdNotificationSeen")
	}
	if checkNeverNotifiedPurchase_LastNotificationSeen != true {
		t.Errorf("Expected notification missing: checkNeverNotifiedPurchase_LastNotificationSeen")
	}
	if checkFifteenDayPurchase_SecondNotificationSeen != true {
		t.Errorf("Expected notification missing: checkFifteenDayPurchase_SecondNotificationSeen")
	}
	if checkFifteenDayPurchase_ThirdNotificationSeen != true {
		t.Errorf("Expected notification missing: checkFifteenDayPurchase_ThirdNotificationSeen")
	}
	if checkFifteenDayPurchase_LastNotificationSeen != true {
		t.Errorf("Expected notification missing: checkFifteenDayPurchase_LastNotificationSeen")
	}
	if checkFourtyDayPurchase_ThirdNotificationSeen != true {
		t.Errorf("Expected notification missing: checkFourtyDayPurchase_ThirdNotificationSeen")
	}
	if checkFourtyDayPurchase_LastNotificationSeen != true {
		t.Errorf("Expected notification missing: checkFourtyDayPurchase_LastNotificationSeen")
	}
	if checkFourtyFourDayPurchase_LastNotificationSeen != true {
		t.Errorf("Expected notification missing: checkFourtyFourDayPurchase_LastNotificationSeen")
	}
}

// SALES
func TestPerformTaskCreatesVendorDisputeTimeoutNotifications(t *testing.T) {
	// Start each sale 50 days ago and have the lastNotifiedAt at a day after
	// each notification is suppose to be sent. With no notifications already queued,
	// it should produce all the old notifications up to the most recent one expected
	var (
		broadcastChannel = make(chan repo.Notifier, 0)
		timeStart        = time.Now().Add(time.Duration(-50*24) * time.Hour)
		twelveHours      = time.Duration(12) * time.Hour
		fourtyFiveDays   = time.Duration(45*24) * time.Hour

		// Produces notification for 45 days
		neverNotified = &repo.SaleRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "neverNotified",
			OrderState:     pb.OrderState(pb.OrderState_FULFILLED),
			Timestamp:      timeStart,
			LastNotifiedAt: time.Unix(0, 0),
		}
		// Produces no notifications as all have already been created
		notifiedUpToFourtyFiveDays = &repo.SaleRecord{
			Contract:       factory.NewDisputeableContract(),
			OrderID:        "notifiedUpToFourtyFiveDays",
			OrderState:     pb.OrderState(pb.OrderState_FULFILLED),
			Timestamp:      timeStart,
			LastNotifiedAt: timeStart.Add(fourtyFiveDays + twelveHours),
		}
		// Produces no notifications as contract is undisputeable
		neverNotifiedButUndisputeable = &repo.SaleRecord{
			Contract:       factory.NewUndisputeableContract(),
			OrderID:        "neverNotifiedButUndisputeable",
			OrderState:     pb.OrderState(pb.OrderState_FULFILLED),
			Timestamp:      timeStart,
			LastNotifiedAt: time.Unix(0, 0),
		}
		existingRecords = []*repo.SaleRecord{
			neverNotified,
			notifiedUpToFourtyFiveDays,
			neverNotifiedButUndisputeable,
		}

		appSchema = schema.MustNewCustomSchemaManager(schema.SchemaContext{
			DataPath:        schema.GenerateTempPath(),
			TestModeEnabled: true,
		})
	)
	neverNotified.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "never-tinyimagehashOne", Small: "never-smallimagehashOne"}}
	notifiedUpToFourtyFiveDays.Contract.VendorListings[0].Item.Images = []*pb.Listing_Item_Image{{Tiny: "fourtyfive-tinyimagehashOne", Small: "fourtyfive-smallimagehashOne"}}

	if err := appSchema.BuildSchemaDirectories(); err != nil {
		t.Fatal(err)
	}
	defer appSchema.DestroySchemaDirectories()
	if err := appSchema.InitializeDatabase(); err != nil {
		t.Fatal(err)
	}
	database, err := appSchema.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}

	m := jsonpb.Marshaler{
		EnumsAsInts:  false,
		EmitDefaults: true,
		Indent:       "    ",
		OrigName:     false,
	}
	for _, r := range existingRecords {
		contractData, err := m.MarshalToString(r.Contract)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.Exec("insert into sales (orderID, contract, state, timestamp, lastNotifiedAt) values (?, ?, ?, ?, ?)", r.OrderID, contractData, int(r.OrderState), int(r.Timestamp.Unix()), int(r.LastNotifiedAt.Unix()))
		if err != nil {
			t.Fatal(err)
		}
	}

	var (
		closeAsyncChannelVerifier = make(chan bool, 0)
		broadcastCount            = 0
	)
	go func() {
		for {
			select {
			case n := <-broadcastChannel:
				notifier, ok := n.(repo.Notifier)
				if !ok {
					t.Errorf("unable to cast as Notifier: %+v", n)
				}
				t.Logf("notification received: %s", notifier)
				broadcastCount += 1
			case <-closeAsyncChannelVerifier:
				return
			}
		}
	}()

	datastore := db.NewSQLiteDatastore(database, new(sync.Mutex))
	worker := &recordAgingNotifier{
		datastore: datastore,
		broadcast: broadcastChannel,
		logger:    logging.MustGetLogger("testRecordAgingNotifier"),
	}

	worker.PerformTask()

	// Verify Notifications received in channel
	closeAsyncChannelVerifier <- true
	if broadcastCount != 1 {
		t.Error("Expected 1 notifications to be broadcast, found", broadcastCount)
	}

	// Verify NotificationRecords in datastore
	rows, err := database.Query("select orderID, lastNotifiedAt from sales")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var (
			orderID        string
			lastNotifiedAt int64
		)
		if err := rows.Scan(&orderID, &lastNotifiedAt); err != nil {
			t.Fatal(err)
		}
		switch orderID {
		case neverNotified.OrderID:
			durationFromActual := time.Now().Sub(time.Unix(lastNotifiedAt, 0))
			if durationFromActual > (time.Duration(5) * time.Second) {
				t.Errorf("Expected %s to have lastNotifiedAt set when executed, was %s", orderID, time.Unix(lastNotifiedAt, 0).String())
			}
		case notifiedUpToFourtyFiveDays.OrderID:
			if lastNotifiedAt != notifiedUpToFourtyFiveDays.LastNotifiedAt.Unix() {
				t.Error("Expected notifiedUpToFourtyFiveDays to not update LastNotifiedAt")
			}
		case neverNotifiedButUndisputeable.OrderID:
			if lastNotifiedAt != neverNotifiedButUndisputeable.LastNotifiedAt.Unix() {
				t.Error("Expected notifiedUpToFourtyFiveDays to not update LastNotifiedAt")
			}
		default:
			t.Error("Unexpected dispute case")
		}
	}

	var count int64
	err = database.QueryRow("select count(*) from notifications").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("Expected 1 notification to be produced, but found %d", count)
	}

	rows, err = database.Query("select notifID, serializedNotification, timestamp from notifications")
	if err != nil {
		t.Fatal(err)
	}

	var (
		checkNeverNotifiedSale_LastNotificationSeen bool

		firstInterval_ExpectedExpiresIn = uint(0)
	)
	for rows.Next() {
		var (
			nID, nJSON string
			nTimestamp sql.NullInt64
			n          *repo.Notification
		)
		if err = rows.Scan(&nID, &nJSON, &nTimestamp); err != nil {
			t.Error(err)
			continue
		}
		if err := json.Unmarshal([]byte(nJSON), &n); err != nil {
			t.Error("Failed unmarshalling notification:", err.Error())
			continue
		}
		var (
			refID     = n.NotifierData.(repo.VendorDisputeTimeout).OrderID
			expiresIn = n.NotifierData.(repo.VendorDisputeTimeout).ExpiresIn
			thumbnail = n.NotifierData.(repo.VendorDisputeTimeout).Thumbnail
		)
		if refID == neverNotified.OrderID && expiresIn == firstInterval_ExpectedExpiresIn {
			assertThumbnailValuesAreSet(t, thumbnail, neverNotified.Contract)
			checkNeverNotifiedSale_LastNotificationSeen = true
			continue
		}
	}

	if checkNeverNotifiedSale_LastNotificationSeen != true {
		t.Errorf("Expected notification missing: checkNeverNotifiedSale_LastNotificationSeen")
	}
}

func assertThumbnailValuesAreSet(t *testing.T, actualThumbnails repo.Thumbnail, contract *pb.RicardianContract) {
	if len(contract.VendorListings) == 0 {
		t.Error("Expected contract to have VendorListings but was empty. Unable to assert Thumbnail values.")
		return
	}
	if len(contract.VendorListings[0].Item.Images) == 0 {
		t.Error("Expected contract to have Item Images but was empty. Unable to assert Thumbnail values.")
		return
	}
	var (
		expectedTinyThumbnail  = contract.VendorListings[0].Item.Images[0].Tiny
		expectedSmallThumbnail = contract.VendorListings[0].Item.Images[0].Small
	)
	if expectedTinyThumbnail != actualThumbnails.Tiny {
		t.Error("Expected notification to include the tiny thumbnail")
		t.Error("Actual:", actualThumbnails.Tiny, "Expected:", expectedTinyThumbnail)
		t.Logf("Contract: %+v\n", contract)
	}
	if expectedSmallThumbnail != actualThumbnails.Small {
		t.Error("Expected notification to include the small thumbnail")
		t.Error("Actual:", actualThumbnails.Small, "Expected:", expectedSmallThumbnail)
		t.Logf("Contract: %+v\n", contract)
	}
}
