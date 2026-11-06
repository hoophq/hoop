(ns webapp.sessions.components.session-header-test
  (:require
   [cljs.test :refer-macros [deftest is testing]]
   [webapp.sessions.components.session-header :as session-header]))

(deftest redacted-params?-test
  (testing "a password or file value redacted by the gateway"
    (is (true? (session-header/redacted-params?
                "{\"secret\":\"[param redacted. type = password]\",\"name\":\"x\"}")))
    (is (true? (session-header/redacted-params?
                "{\"cert\":\"[param redacted. type = file, size = 8 byte(s)]\"}"))))
  (testing "plain values re-run as before"
    (is (false? (session-header/redacted-params? "{\"name\":\"/app/db/password\"}")))
    (is (false? (session-header/redacted-params? "{}"))))
  (testing "a missing or broken label does not block the re-run"
    (is (false? (session-header/redacted-params? nil)))
    (is (false? (session-header/redacted-params? "not json")))))
