(ns webapp.components.toast
  (:require
   ["@radix-ui/themes" :refer [Box Text]]
   ["lucide-react" :refer [AlertCircle CheckCircle ChevronDown ChevronUp X]]
   ["sonner" :refer [toast]]
   [clojure.string :as cs]
   [reagent.core :as r]))


(defn format-json-colored [obj]
  (when (map? obj)
    [:div
     (for [[key value] obj]
       ^{:key (name key)}
       [:div
        [:> Text {:size "1" :class "text-warning-10"} (str "\"" (cs/capitalize (name key)) "\"")]
        [:> Text {:size "1" :class "text-gray-1"} ": "]
        [:> Text {:size "1" :class "text-info-10"} (str "\"" (cs/capitalize (str value)) "\"")]
        [:> Text {:size "1" :class "text-gray-1"} ","]])]))

(defn get-toast-styles [toast-type]
  (case toast-type
    :success {:icon-color "text-success-11"}
    :error {:icon-color "text-error-11"}
    {:icon-color "text-gray-11"}))

(defn toast-component [{:keys [id title description type details]}]
  (let [expanded? (r/atom false)
        has-details? (and (= type :error) (some? details))
        styles (get-toast-styles type)]

    (fn []
      [:> Box {:class (str "flex flex-col rounded-4 shadow-lg ring-1 "
                           "ring-black/5 w-[364px] p-4 bg-white")}

       [:> Box {:class "flex items-start justify-between"}
        [:> Box {:class "flex items-start gap-3"}
         [:> Box {:class (str "flex-shrink-0 " (:icon-color styles))}
          (case type
            :success [:> CheckCircle {:size "20"}]
            :error [:> AlertCircle {:size "20"}]
            nil)]

         [:> Box {:class "flex-1 min-w-0"}
          [:> Text {:as "p" :size "2" :class "text-gray-12"}
           title]
          (when description
            [:> Text {:as "p" :size "2" :class "text-gray-12"}
             description])

          (when has-details?
            [:> Box {:class "mt-1 flex items-center gap-1 cursor-pointer"
                     :on-click #(swap! expanded? not)}
             [:> Text {:size "2"
                       :weight "medium"
                       :class "text-gray-12"}
              (if @expanded? "Hide details" "View details")]
             (if @expanded? [:> ChevronUp {:size 16}] [:> ChevronDown {:size 16}])])]]

        [:> Box {:class "flex-shrink-0 cursor-pointer"
                 :on-click #(toast.dismiss id)}
         [:> X {:size 16}]]]

       (when (and has-details? @expanded?)
         [:> Box {:class "mt-3 p-3 bg-gray-12 rounded-b-4 -m-4"}
          [:div {:class "text-xs font-mono"}
           (format-json-colored details)]])])))

(defn custom-toast [toast-data toast-props]
  (toast.custom
   (fn [id]
     (r/as-element
      [toast-component (assoc toast-data :id id)])) toast-props))

(defn toast-success
  ([title] (toast-success title nil))
  ([title description]
   (custom-toast {:type :success
                  :title title
                  :description description}

                 #js{})))

(defn toast-error
  ([title] (toast-error title nil nil))
  ([title description] (toast-error title description nil))
  ([title description details]
   (custom-toast {:type :error
                  :title title
                  :description description
                  :details details}

                 #js{:duration 10000})))
