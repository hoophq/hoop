(ns webapp.reviews.review-detail
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@radix-ui/themes" :refer [Button Box Flex Text Tooltip]]
            ["clipboard" :as clipboardjs]
            ["lucide-react" :refer [Download FileDown]]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]
            [webapp.components.icon :as icon]
            [webapp.components.loaders :as loaders]
            [webapp.components.popover :as popover]
            [webapp.components.tooltip :as tooltip]
            [webapp.components.user-icon :as user-icon]
            [webapp.formatters :as formatters]
            [webapp.routes :as routes]
            [webapp.utilities :as utilities]))

(defn- add-review-popover [add-review-cb]
  [:div
   {:class "flex gap-small p-regular"}
   [button/secondary {:text "Reject"
                      :variant :small
                      :on-click #(add-review-cb "rejected")}]
   [button/primary {:text "Approve"
                    :variant :small
                    :on-click #(add-review-cb "approved")}]])

(defmulti ^:private review-status-icon identity)
(defmethod ^:private review-status-icon "PENDING" [] "waiting-circle-yellow")
(defmethod ^:private review-status-icon "APPROVED" [] "check-black")
(defmethod ^:private review-status-icon "REJECTED" [] "close-red")

(defn- review-group-item [group session user]
  (let [add-review-popover-open? (r/atom false)
        add-review (fn [status]
                     (rf/dispatch [:reviews-plugin->add-review
                                   session
                                   status
                                   (:group group)])
                     (reset! add-review-popover-open? false))]
    [:> Box {:class "flex w-full relative items-center gap-small text-xs"}
     [:> Box
      [icon/regular {:size 4
                     :icon-name "user-group"}]]
     [tooltip/truncate-tooltip {:text (:group group)}]
     [:> Box
      [:span {:class "text-xxs italic text-gray-500 text-right"}
       (:status group)]
      (when (or (= (:status group) "APPROVED")
                (= (:status group) "REJECTED"))
        [:> Box {:class "text-xxs italic text-gray-500 text-right max-w-[100px]"}
         [tooltip/truncate-tooltip {:text (-> group :reviewed_by :email)}]])]
     [:> Box
      [icon/regular {:size 4
                     :icon-name (review-status-icon
                                 (cs/upper-case (:status group)))}]]
     [popover/right {:open @add-review-popover-open?
                     :component [add-review-popover add-review]
                     :on-click-outside #(reset! add-review-popover-open? false)}]]))

(defn- loading-player []
  [:div {:class "flex gap-small items-center justify-center py-large"}
   [:span {:class "italic text-xs text-gray-600"}
    "Loading data for this session"]
   [loaders/simple-loader {:size 4}]])

(defn- large-payload-warning [{:keys [session]}]
  [:> Flex {:height "400px"
            :direction "column"
            :gap "5"
            :class "p-[--space-5] bg-[--gray-2] rounded-[9px]"
            :align "center"
            :justify "center"}
   [:> FileDown {:size 48 :color "gray"}]
   [:> Text {:size "3" :class "text-[--gray-11]"}
    "This result is not currently supported to view in browser."]
   [:> Button {:size "3"
               :variant "solid"
               :on-click #(rf/dispatch [:audit->session-file-generate
                                        (:id session)
                                        "txt"])}
    "Download file"
    [:> Download {:size 18}]]])

(defn- event-stream-content [session]
  (let [has-large-payload? false
        verb (:verb session)
        review (:review session)]
    (if has-large-payload?
      [large-payload-warning {:session session}]

      [:div {:class "h-full px-small"}
       (if (= verb "exec")
         ;; Mostramos apenas informações sobre o pedido de execução,
         ;; não o resultado, pois está pendente de aprovação
         [:div {:class "flex flex-col items-center justify-center h-64 text-center"}
          [:> hero-outline-icon/DocumentTextIcon {:class "h-12 w-12 text-gray-400 mb-4"}]
          [:div {:class "text-gray-500 text-sm font-medium"}
           "Execution request awaiting approval"]
          [:div {:class "text-gray-400 text-xs mt-2"}
           "This command will be executed upon approval"]
          [:div {:class "mt-4 flex items-center gap-2 text-xs"}
           [:span {:class "text-gray-500"}
            "Review status:"]
           [:span
            {:class (str "text-xxs rounded-full px-2 py-1 "
                         (case (-> review :status)
                           "PENDING" "bg-yellow-100 text-yellow-800"
                           "APPROVED" "bg-green-100 text-green-800"
                           "REJECTED" "bg-red-100 text-red-800"
                           "bg-gray-100 text-gray-800"))}
            (-> review :status)]]]

         ;; Caso não seja exec, mostramos uma mensagem de sessão ativa
         [:div {:class "flex flex-col items-center justify-center h-64 text-center"}
          [:> hero-outline-icon/ComputerDesktopIcon {:class "h-12 w-12 text-gray-400 mb-4"}]
          [:div {:class "text-gray-500 text-sm font-medium"}
           "Connection request awaiting approval"]
          [:div {:class "text-gray-400 text-xs mt-2"}
           "User is requesting access to this resource"]
          (when review
            [:div {:class "mt-4 flex items-center gap-2 text-xs"}
             [:span {:class "text-gray-500"}
              "Review status:"]
             [:span
              {:class (str "text-xxs rounded-full px-2 py-1 "
                           (case (-> review :status)
                             "PENDING" "bg-yellow-100 text-yellow-800"
                             "APPROVED" "bg-green-100 text-green-800"
                             "REJECTED" "bg-red-100 text-red-800"
                             "bg-gray-100 text-gray-800"))}
              (-> review :status)]])])])))

(defn review-details-page [session]
  (let [user (rf/subscribe [:users->current-user])
        add-review-popover-open? (r/atom false)
        clipboard-url (new clipboardjs ".copy-to-clipboard-url")]
    (fn []
      (let [user-name (:user_name session)
            connection-name (:connection session)
            review (:review session)
            review-groups-data (:review_groups_data review)
            session-type (:type session)
            start-date (:start_date session)
            end-date (:end_date session)
            verb (:verb session)

            killing-status (r/atom :ready)
            can-review? (and
                         (some #(= "PENDING" (:status %))
                               review-groups-data)
                         (some (fn [review-group]
                                 (some #(= (:group review-group) %)
                                       (-> @user :data :groups)))
                               review-groups-data))
            add-review-cb (fn [status]
                            (rf/dispatch [:reviews-plugin->add-review
                                          session
                                          status])
                            (reset! add-review-popover-open? false))
            in-progress? (or (= end-date nil)
                             (= end-date ""))
            can-kill-session? (and (= (:status session) "open")
                                   (= (-> review :status) "APPROVED"))
            kill-session (fn []
                           (reset! killing-status :loading)
                           (rf/dispatch [:reviews-plugin->kill-session session killing-status]))
            _ (.on clipboard-url "success" #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}]))]
        [:div
     ;; Header
         [:header {:class "mb-regular mr-large"}
          [:div {:class "flex"}
           [:div {:class "flex flex-col lg:flex-row flex-grow gap-small lg:items-baseline"}
            [:div {:class "flex flex-col"}
             [h/h2 connection-name]
             [:div {:class "text-sm flex flex-grow gap-regular"}
              [:span {:class "text-gray-500"}
               "type:"]
              [:span {:class "font-bold"}
               session-type]]]

            (when in-progress?
              [:div {:class "flex gap-small lg:justify-end items-center h-full lg:ml-large"}
               [:div {:class "rounded-full w-1.5 h-1.5 bg-green-500"}]
               [:span {:class "text-xs text-gray-500"}
                "This session has pending items"]])]

           [:div {:class "relative flex gap-2.5 items-start pr-3"}
            (when can-kill-session?
              [:div {:class "relative group"}
               [:> Tooltip {:content "Kill Session"}
                [:div {:class "rounded-full p-2 bg-red-100 hover:bg-red-200 transition cursor-pointer"
                       :on-click kill-session}
                 (if (= @killing-status :loading)
                   [loaders/simple-loader {:size 2}]
                   [:> hero-outline-icon/StopIcon {:class "h-5 w-5 text-red-600"}])]]])

            [:div {:class "relative group"}
             [:> Tooltip {:content "Copy link"}
              [:div {:class "rounded-full p-2 bg-gray-100 hover:bg-gray-200 transition cursor-pointer copy-to-clipboard-url"
                     :data-clipboard-text (str (-> js/document .-location .-origin)
                                               (routes/url-for :sessions)
                                               "/" (:id session))}
               [:> hero-outline-icon/ClipboardDocumentIcon {:class "h-5 w-5 text-gray-600"}]]]]]]]

     ;; Information Grid
         [:section {:class "grid grid-cols-1 gap-regular pb-regular lg:grid-cols-3"}
          [:div {:class "col-span-1 flex gap-large items-center"}
           [:div {:class "flex flex-grow gap-regular items-center"}
            [user-icon/initials-black user-name]
            [:span
             {:class "text-gray-800 text-sm"}
             user-name]]]

          [:div {:class (str "flex flex-col gap-small self-center justify-center"
                             " rounded-lg bg-gray-100 p-3")}
           [:div
            {:class "flex items-center gap-regular text-xs"}
            [:span
             {:class "flex-grow text-gray-500"}
             "start:"]
            [:span
             (formatters/time-parsed->full-date start-date)]]
           (when-not (and (= verb "exec") in-progress?)
             [:div
              {:class "flex items-center justify-end gap-regular text-xs"}
              [:span
               {:class "flex-grow text-gray-500"}
               "end:"]
              [:span
               (formatters/time-parsed->full-date end-date)]])
           (when (> (or (:access_duration review) 0) 0)
             [:div {:class "flex items-center gap-small"}
              [:span {:class "text-gray-500"}
               "session time:"]
              [:span {:class "font-bold"}
               (formatters/time-elapsed (/ (:access_duration review) 1000000))]])]

      ;; Reviewers section
          [:div {:id "session-reviews" :class "self-center"}
           [:header {:class "relative flex text-xs text-gray-800 mb-small"}
            [:span {:class "flex-grow font-bold"} "Reviewers"]
            [:<>
             (when can-review?
               [:span {:class (str "flex items-center cursor-pointer "
                                   "text-xxs text-blue-500 font-semibold")
                       :on-click #(reset! add-review-popover-open? true)}
                [:span "Add your review"]
                [icon/regular {:size 5
                               :icon-name "cheveron-down-blue"}]])

             (println @add-review-popover-open?)
             [popover/right {:open @add-review-popover-open?
                             :component [add-review-popover add-review-cb]
                             :on-click-outside #(reset! add-review-popover-open? false)}]]]

           (when (empty? review-groups-data)
             [:div
              {:class "py-small text-xs italic text-gray-500 text-left"}
              "No review info"])
           [:div {:class "rounded-lg w-full flex flex-col gap-2"}
            (doall
             (for [group review-groups-data]
               ^{:key (:id group)}
               [review-group-item group session @user]))]]]

     ;; Script section
         (when (not (cs/blank? (-> session :script :data)))
           [:section {:id "session-script"}
            [:div
             {:class (str "w-full max-h-40 overflow-auto p-regular whitespace-pre "
                          "rounded-lg bg-gray-100 "
                          "text-xs text-gray-800 font-mono")}
             [:article (-> session :script :data)]]])

     ;; Event Stream section
         [:section {:id "session-event-stream"
                    :class "pt-regular"}
          [event-stream-content session]]]))))

(defmulti item-view identity)
(defmethod item-view :opened [_ review-details]
  (review-details-page (:review review-details)))

(defmethod item-view :default [_]
  [:div.flex.justify-center.items-center.h-full
   [:span.text-xl.text-gray-400 "No review selected"]])

(defmethod item-view :loading [_ task-details]
  (item-view :opened task-details))

(defn review-detail []
  (let [active-review (rf/subscribe [:reviews-plugin->review-details])]
    (fn []
      [item-view
       (:status @active-review)
       @active-review])))

