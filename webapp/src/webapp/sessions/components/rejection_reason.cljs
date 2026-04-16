(ns webapp.sessions.components.rejection-reason
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text]]
   ["lucide-react" :refer [OctagonX]]
   [clojure.string :as cs]))

(defn main [{:keys [session]}]
  (let [review-status    (-> session :review :status)
        rejection-reason (get-in session [:review :rejection_reason])
        reviewer-email   (->> (get-in session [:review :review_groups_data])
                              (filter #(= "REJECTED" (:status %)))
                              first
                              :reviewed_by
                              :email)]
    (when (= "REJECTED" review-status)
      [:> Box {:class "flex items-baseline justify-between w-full bg-error-2 rounded-3 px-4 py-3"}
       [:> Box {:class "flex items-center gap-2"}
        [:> OctagonX {:size 16 :class "text-error-12 shrink-0"}]
        [:> Text {:size "2" :weight "bold" :class "text-error-12"}
         "Reject Details"]]
       [:> Flex {:direction "column" :align "end" :gap "1"}
        (when (not (cs/blank? rejection-reason))
          [:> Text {:size "2" :class "whitespace-pre-wrap text-right text-error-12"}
           rejection-reason])
        (when reviewer-email
          [:> Text {:size "2" :weight "bold" :class "text-error-12"}
           (str "Rejected by " reviewer-email)])]])))
