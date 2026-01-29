(ns webapp.audit.views.data-masking-analytics
  (:require 
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Box Flex Text Callout Link]]
   ["lucide-react" :refer [ChevronDown Sparkles LayoutList ArrowRightLeft ArrowUpRight]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.config :as config]
   [webapp.utilities :as utilities]))

(defn no-presidio-callout []
  [:> Callout.Root {:size "2"
                    :class "w-full bg-[--violet-2] p-3"}
   [:> Callout.Icon
    [:> Sparkles {:size 16
                  :color "var(--violet-9)"}]]
   [:> Callout.Text {:class "text-gray-12"}
    [:> Text {:as "p" :size "2" :weight "bold" :class "mb-2"}
     "Unlock AI-Powered Data Masking"]
    [:> Text {:as "p" :size "2" :class "mb-2"}
     "Redact sensitive fields on the fly to reduce exposure risk and keep your data pipelines compliant."]
    [:> Flex {:direction "column" :gap "2"}
     [:> Link {:href "#"
               :class "text-primary-10 flex items-center gap-1 w-fit no-underline font-medium"
               :on-click (fn [e]
                           (.preventDefault e)
                           (rf/dispatch [:close-modal])
                           (rf/dispatch [:navigate :ai-data-masking]))}
      "Configure it on AI Data Masking"
      [:> ArrowUpRight {:size 16}]]
     [:> Link {:href (get-in config/docs-url [:features :ai-datamasking])
               :target "_blank"
               :class "text-primary-10 flex items-center gap-1 w-fit no-underline font-medium"}
      "Go to AI Data Masking Docs"
      [:> ArrowUpRight {:size 16}]]]]])

(defn- build-analytics-report [session-report session]
  (let [report-items (get-in session-report [:data :items])
        report-total (get-in session-report [:data :total_redact_count])
        report-ready? (= :ready (:status session-report))
        has-report-data? (and report-ready?
                              (or (seq report-items)
                                  (pos? (or report-total 0))))]
    (if has-report-data?
      session-report
      (let [data-analyzer (or (get-in session [:metrics :data_analyzer]) {})
            items (map (fn [[info-type count]]
                         {:info_type (name info-type)
                          :count count})
                       data-analyzer)
            total-redact (reduce + 0 (vals data-analyzer))]
        {:data {:items items
                :total_redact_count total-redact}}))))

(defn data-masking-analytics [session-report & {:keys [title subtitle hide-summary?]}]
  (let [redacted-types (map #(utilities/sanitize-string (:info_type %))
                            (-> session-report :data :items))
        total-redact (-> session-report :data :total_redact_count)
        total-items-text (str total-redact " " (if (<= total-redact 1) "item" "items"))
        count-less-1 (- (count redacted-types) 1)
        redacted-types-list (cs/join ", " redacted-types)
        redacted-types-display (if (pos? (count redacted-types))
                                 (str (count redacted-types)
                                      " (" redacted-types-list ")")
                                 "-")
        redacted-types-summary (if (pos? (count redacted-types))
                                 (let [first-type (first redacted-types)]
                                   (if (>= count-less-1 1)
                                     (str (count redacted-types) " (" first-type " + " count-less-1 " more)")
                                     (str (count redacted-types) " (" first-type ")")))
                                 "0")
        display-title (or title "AI Data Masking")]
    [:> (.-Root Accordion) {:type "single"
                            :collapsible true
                            :class "w-full p-3 bg-[--violet-2] rounded-md"}
     [:> (.-Item Accordion) {:value "ai-data-masking-analytics"
                             :className "border-none"}
      [:> (.-Header Accordion)
       [:> (.-Trigger Accordion) {:className "group flex w-full items-center justify-between gap-3 text-left text-base font-semibold text-sm focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
        [:> Flex {:direction "column" :align "start" :gap "1" :class "min-w-0 flex-1"}
         [:> Flex {:align "center" :gap "2" :class "min-w-0"}
          [:> Sparkles {:size 16
                        :class "shrink-0"
                        :color "var(--violet-9)"}]
          [:> Text {:size "2" :weight "bold"} display-title]]
         (when subtitle
           (if (string? subtitle)
             [:> Text {:size "1" :class "text-gray-11"}
              subtitle]
             [:> Box {:class ""}
              subtitle]))]
       [:> Flex {:align "center" :gap "4" :class "flex-wrap justify-end text-xs"}
        (when-not hide-summary?
          [:> Box {:class "group-data-[state=open]:hidden"}
           [:> Text {:size "1"} "Data Categories: "]
           [:> Text {:size "1" :class "font-normal"}
            redacted-types-summary]
           [:> Text {:size "1"} " Volume of Data: "]
           [:> Text {:size "1" :class "font-normal"}
            total-items-text]])
         [:> ChevronDown {:size 16
                          :className "text-gray-10 transition-transform group-data-[state=open]:rotate-180 shrink-0"}]]]]
      [:> (.-Content Accordion) {:className "mt-3"}
       [:> Box {:class "grid grid-cols-2 gap-2 text-xs"}
        [:> Box {:class "flex flex-col justify-center items-center gap-1 rounded-md bg-[--violet-3] p-2"}
         [:> LayoutList {:size 16
                         :class "shrink-0"
                         :color "var(--violet-9)"}]
         [:> Text {:size "1"} "Data Categories"]
         [:> Text {:size "2" :class "font-semibold"}
          redacted-types-display]]
        [:> Box {:class "flex flex-col justify-center items-center gap-1 rounded-md bg-[--violet-3] p-2"}
         [:> ArrowRightLeft {:size 16
                             :class "shrink-0"
                             :color "var(--violet-9)"}]
         [:> Text {:size "1"} "Volume of Data"]
         [:> Text {:size "2" :class "font-semibold"}
          total-items-text]]]]]]))

(defn main [{:keys [session]}]
  (let [user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        session-report (rf/subscribe [:reports->session])
        free-license? (-> @user :data :free-license?)
        redact-provider (-> @gateway-info :data :redact_provider)
        analytics-report (build-analytics-report @session-report session)]
    (cond
      (not= redact-provider "mspresidio")
      [no-presidio-callout]

      free-license?
      [data-masking-analytics
       analytics-report
       {:title "Enable AI-Powered Data Masking"
        :hide-summary? true
        :subtitle [:> Text {:size "2" :class "font-normal"}
                   "We detected sensitive data that could protected with automated data masking. "
                   [:> Link {:href "#"
                             :class "text-primary-10 inline-flex items-center no-underline font-medium"
                             :on-click (fn [e]
                                         (.preventDefault e)
                                         (rf/dispatch [:close-modal])
                                         (rf/dispatch [:navigate :ai-data-masking]))}
                    "Configure it on AI Data Masking"
                    [:> ArrowUpRight {:size 14}]]]}]

      :else
      [data-masking-analytics analytics-report])))