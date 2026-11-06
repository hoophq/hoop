(ns webapp.audit.session-details-bridge-test
  (:require
   [cljs.test :refer [deftest is testing use-fixtures]]
   [re-frame.core :as rf]
   [re-frame.registrar :as registrar]
   [webapp.audit.session-details-bridge :as bridge]))

(def ^:private open-event "hoop:session-details-open")

(use-fixtures :each
  {:after (fn [] (js-delete js/window "__hoopReactShellPresent"))})

(defn- dispatched-by [f]
  (let [sent (atom [])]
    (with-redefs [rf/dispatch #(swap! sent conj %)]
      (f))
    @sent))

(deftest emit-falls-back-to-the-clojurescript-details
  (let [emit (registrar/get-handler :fx :session-details-bridge/emit)]
    (testing "nobody takes the event: the ClojureScript details open"
      (is (= [[:session-details/open-cljs {:id "s1"}]]
             (dispatched-by #(emit {:id "s1"})))))
    (testing "the React details take the event: nothing else opens"
      (let [take (fn [event] (.preventDefault event))]
        (.addEventListener js/window open-event take)
        (try
          (is (= [] (dispatched-by #(emit {:id "s1"}))))
          (finally
            (.removeEventListener js/window open-event take)))))))

(deftest open-asks-react-under-the-shell
  (testing "standalone: the ClojureScript details, without the event"
    (is (= {:fx [[:dispatch [:session-details/open-cljs {:id "s1"}]]]}
           (#'bridge/open-fx {:db {}} [:session-details/open {:id "s1"}]))))
  (testing "under the shell: the event, whatever this app's copy of the flag says"
    (set! (.-__hoopReactShellPresent js/window) true)
    (is (= {:session-details-bridge/emit {:id "s1"}}
           (#'bridge/open-fx {:db {}} [:session-details/open {:id "s1"}])))))
