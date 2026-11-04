(ns webapp.resources.setup.agent-options-test
  "Create Resource agent picker: name order and search (ENG-1034)."
  (:require
   [cljs.test :refer-macros [deftest is]]
   [webapp.resources.setup.agent-options :as ao]))

(def ^:private agents
  [{:id "1" :name "prod-us"}
   {:id "2" :name "Dev-br"}
   {:id "3" :name "agent10"}
   {:id "4" :name "agent2"}
   {:id "5" :name "prod-eu"}
   {:id "6" :name nil}])

(defn- texts [options] (mapv :text options))

(deftest sorts-by-name-case-insensitive-and-numeric
  (is (= [nil "agent2" "agent10" "Dev-br" "prod-eu" "prod-us"]
         (texts (ao/agent-options agents "" nil)))))

(deftest maps-to-select-options
  (is (= {:value "4" :text "agent2"}
         (second (ao/agent-options agents nil nil)))))

(deftest search-narrows-case-insensitive
  (is (= ["prod-eu" "prod-us"]
         (texts (ao/agent-options agents "  PROD " nil)))))

(deftest selected-agent-stays-while-search-hides-it
  (is (= ["Dev-br" "prod-eu" "prod-us"]
         (texts (ao/agent-options agents "prod" "2")))))

(deftest no-match-returns-only-the-selected-agent
  (is (= [] (ao/agent-options agents "zzz" nil)))
  (is (= ["agent10"] (texts (ao/agent-options agents "zzz" "3")))))
