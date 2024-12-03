(ns webapp.settings.license.panel
  (:require
    [re-frame.core :as rf]
    ["@radix-ui/themes" :refer [Table Flex Box Text Heading Card]]
    ["lucide-react" :refer [CircleCheck]]
    [webapp.components.headings :as h]))

(defmulti license-status-text identity)
(defmethod license-status-text "oss" [_]
  "Open Source License")
(defmethod license-status-text "enterprise" [_]
  "Enterprise License")
(defmethod license-status-text :default [_]
  "Loading...")
(def months {0 "Jan", 1 "Feb", 2 "Mar", 3 "Apr", 4 "May", 5 "Jun", 6 "Jul", 7 "Aug", 8 "Sep", 9 "Oct", 10 "Nov", 11 "Dec"})

(defn get-formatted-date [timestamp]
  (let [date (new js/Date (* timestamp 1000))
        day (.getDate date)]
    (str (.getFullYear date)
         "/"
         (get months (.getMonth date))
         "/"
         (when (< day 10) "0")
         (.getDate date))))

(defn main []
  (let [gateway-info (rf/subscribe [:gateway->info])]
    (fn []
      (let [license-info (-> @gateway-info :data :license_info)
            license-type (:type license-info)
            issued-date (:issued_at license-info)
            expiration-date (:expire_at license-info)]
        [:div
         [:> Flex {:class "mb-10"
                   :as "header"}
          [:> Box {:flexGrow "1"}
           [h/PageHeader {:text "License Management"}]
           [:> Text {:size "5" :class "text-[--gray-11]"}
            "Manage your organization's license"]]]
         [:> Flex {:direction :column
                   :gap "8"}
          [:> Flex {:direction :column
                    :gap "4"}
           [:> Box
            [:> Heading {:size "5"}
             "Information"]]
           [:> Box
            [:> Table.Root {:variant "surface"}
             [:> Table.Header
              [:> Table.Row
               [:> Table.ColumnHeaderCell "Type"]
               [:> Table.ColumnHeaderCell "Issued"]
               [:> Table.ColumnHeaderCell "Expiration"]
               ]]
             [:> Table.Body
              [:> Table.Row
               [:> Table.Cell
                (license-status-text license-type)]
               [:> Table.Cell
                [:> Text {:size "1"}
                 (get-formatted-date issued-date)]]
               [:> Table.Cell
                [:> Text {:size "1"}
                 (cond
                   (= license-type "oss") "N/A"
                   (= license-type "enterprise") (get-formatted-date expiration-date)
                   :else "Loading...")]]]]]]]
          [:> Flex {:direction :column
                    :gap "4"}
           [:> Box
            [:> Heading {:size "5" :as "h3"}
             "License"]]
           [:> Box
            [:> Table.Root {:variant "surface"}
             [:> Table.Body
              [:> Table.Row
               [:> Table.ColumnHeaderCell
                [:> Text {:size "1"} "Verified hostname"]]
               [:> Table.Cell
                [:> Text (:verified_host license-info)]]]
              [:> Table.Row
               [:> Table.ColumnHeaderCell
                [:> Text {:size "1"} "Key ID"]]
               [:> Table.Cell
                (:key_id license-info)]]
              [:> Table.Row
               [:> Table.ColumnHeaderCell
                [:> Text {:size "1"} "License Key"]]
               [:> Table.Cell
                "*****"]]
              ]]]]]]))))
